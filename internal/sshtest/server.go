// Package sshtest is a small in-process SSH server used by integration tests
// across packages. It accepts a single username + password and runs a fixed
// command table (uname, hostname, /etc/os-release, echo, stream-N, sleep, fail).
//
// It exists outside _test.go so cross-package integration tests (api, tasks,
// cmd/bm) can drive a real SSH endpoint without depending on Docker or sshd.
package sshtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// Server is a minimal in-process SSH server.
type Server struct {
	listener net.Listener
	addr     string
	hostKey  gossh.Signer
	user     string
	password string

	// CmdOverride lets tests intercept specific commands before the default
	// command table runs. Returning ok=true short-circuits the default
	// table; ok=false falls through. Safe for concurrent use; callers
	// should set this before any session arrives.
	CmdOverride func(cmd string) (stdout, stderr string, exit int, ok bool)

	wg     sync.WaitGroup
	stopCh chan struct{}

	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

// Options configures a Server.
type Options struct {
	User     string
	Password string
}

// Start brings up a listener on 127.0.0.1:0 and returns a running Server.
func Start(opts Options) (*Server, error) {
	if opts.User == "" {
		opts.User = "tester"
	}
	if opts.Password == "" {
		opts.Password = "hunter2"
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{
		listener: ln,
		addr:     ln.Addr().String(),
		hostKey:  signer,
		user:     opts.User,
		password: opts.Password,
		stopCh:   make(chan struct{}),
		conns:    map[net.Conn]struct{}{},
	}
	s.wg.Add(1)
	go s.serve()
	return s, nil
}

// User returns the username clients must use.
func (s *Server) User() string { return s.user }

// Password returns the password clients must use.
func (s *Server) Password() string { return s.password }

// HostPort returns the bound address split into host + port.
func (s *Server) HostPort() (string, int) {
	host, portStr, _ := net.SplitHostPort(s.addr)
	var p int
	fmt.Sscanf(portStr, "%d", &p)
	return host, p
}

// Stop shuts the server down. Safe to call multiple times. Forcibly closes
// any active connections so per-session goroutines unblock from `for req
// := range reqs` even when the caller hasn't released its SSH client.
func (s *Server) Stop() {
	select {
	case <-s.stopCh:
		return
	default:
		close(s.stopCh)
	}
	_ = s.listener.Close()
	s.mu.Lock()
	for c := range s.conns {
		_ = c.Close()
	}
	s.conns = map[net.Conn]struct{}{}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Server) trackConn(c net.Conn) {
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) untrackConn(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

func (s *Server) serve() {
	defer s.wg.Done()
	cfg := &gossh.ServerConfig{
		PasswordCallback: func(c gossh.ConnMetadata, pw []byte) (*gossh.Permissions, error) {
			if c.User() == s.user && string(pw) == s.password {
				return &gossh.Permissions{}, nil
			}
			return nil, errors.New("bad credentials")
		},
	}
	cfg.AddHostKey(s.hostKey)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stopCh:
				return
			default:
				continue
			}
		}
		s.wg.Add(1)
		go s.handleConn(conn, cfg)
	}
}

func (s *Server) handleConn(c net.Conn, cfg *gossh.ServerConfig) {
	defer s.wg.Done()
	s.trackConn(c)
	defer s.untrackConn(c)
	_, chans, reqs, err := gossh.NewServerConn(c, cfg)
	if err != nil {
		_ = c.Close()
		return
	}
	go gossh.DiscardRequests(reqs)
	for ch := range chans {
		if ch.ChannelType() != "session" {
			_ = ch.Reject(gossh.UnknownChannelType, "unsupported")
			continue
		}
		channel, requests, err := ch.Accept()
		if err != nil {
			continue
		}
		s.wg.Add(1)
		go s.handleSession(channel, requests)
	}
}

func (s *Server) handleSession(ch gossh.Channel, reqs <-chan *gossh.Request) {
	defer s.wg.Done()
	defer ch.Close()
	for req := range reqs {
		switch req.Type {
		case "exec":
			cmd := parseExecPayload(req.Payload)
			req.Reply(true, nil)
			if s.CmdOverride != nil {
				if stdout, stderr, exit, ok := s.CmdOverride(cmd); ok {
					if stdout != "" {
						_, _ = io.WriteString(ch, stdout)
					}
					if stderr != "" {
						_, _ = io.WriteString(ch.Stderr(), stderr)
					}
					sendExit(ch, exit)
					return
				}
			}
			exit := runFakeCommand(cmd, ch, ch.Stderr())
			sendExit(ch, exit)
			return
		default:
			req.Reply(false, nil)
		}
	}
}

func parseExecPayload(p []byte) string {
	if len(p) < 4 {
		return ""
	}
	n := int(p[0])<<24 | int(p[1])<<16 | int(p[2])<<8 | int(p[3])
	if n+4 > len(p) {
		return string(p[4:])
	}
	return string(p[4 : 4+n])
}

func sendExit(ch gossh.Channel, code int) {
	payload := []byte{0, 0, 0, byte(code)}
	_, _ = ch.SendRequest("exit-status", false, payload)
}

func runFakeCommand(cmd string, stdout io.Writer, stderr io.Writer) int {
	switch {
	case cmd == "uname -a":
		fmt.Fprintln(stdout, "Linux fake 6.6.0-test #1 SMP x86_64 GNU/Linux")
		return 0
	case cmd == "uname -r":
		fmt.Fprintln(stdout, "6.6.0-test")
		return 0
	case cmd == "uname -m":
		fmt.Fprintln(stdout, "x86_64")
		return 0
	case cmd == "hostname":
		fmt.Fprintln(stdout, "fakehost")
		return 0
	case cmd == "id -u":
		fmt.Fprintln(stdout, "1000")
		return 0
	case cmd == "sudo -n true":
		return 0
	case cmd == "true":
		return 0
	case cmd == "command -v dnf":
		fmt.Fprintln(stdout, "/usr/bin/dnf")
		return 0
	case strings.Contains(cmd, "command -v dnf") && strings.Contains(cmd, "command -v yum") && strings.Contains(cmd, "command -v apt-get"):
		// Combined probe used by deploy.DetectPackageManager. Default
		// to an RPM host (dnf available) — tests that need a deb host
		// install a CmdOverride that intercepts this case first.
		fmt.Fprintln(stdout, "/usr/bin/dnf")
		fmt.Fprintln(stdout, "/usr/bin/yum")
		fmt.Fprintln(stdout, "")
		return 0
	case strings.HasPrefix(cmd, "command -v "):
		return 1
	case strings.HasPrefix(cmd, "rpm -q ") || strings.HasPrefix(cmd, "dpkg -s "):
		return 1
	case strings.HasPrefix(cmd, "dpkg-query -W ") || strings.HasPrefix(cmd, "dpkg-deb -f ") || strings.HasPrefix(cmd, "dpkg --compare-versions "):
		// dpkg machinery the Debian path uses. Default to "nothing
		// installed"; tests that need a populated dpkg-query response
		// install a CmdOverride.
		return 0
	case strings.HasPrefix(cmd, "curl -fSL -o /tmp/buckit.deb"):
		return 0
	case strings.HasPrefix(cmd, "apt-get install"):
		return 0
	case strings.HasPrefix(cmd, "ss -ltn"):
		fmt.Fprintln(stdout, "State   Recv-Q Send-Q   Local Address:Port    Peer Address:Port")
		return 0
	case strings.HasPrefix(cmd, "df -B1"):
		fmt.Fprintln(stdout, "Avail")
		fmt.Fprintln(stdout, "5000000000")
		return 0
	case strings.HasPrefix(cmd, "test -f"):
		return 1
	case strings.HasPrefix(cmd, "getent hosts"):
		return 0
	case strings.HasPrefix(cmd, "timeout 2 bash -c"):
		return 0
	case strings.HasPrefix(cmd, "curl -fI"):
		return 0
	case strings.HasPrefix(cmd, "date +%s.%N"):
		fmt.Fprintln(stdout, fmt.Sprintf("%.3f", float64(time.Now().UnixNano())/1e9))
		return 0
	case strings.HasPrefix(cmd, "grep -q MINIO_VOLUMES"):
		return 1
	case strings.HasPrefix(cmd, "curl -fSL"):
		// Artifact download success.
		return 0
	case strings.Contains(cmd, "sha256sum -c -") || strings.Contains(cmd, "shasum -a 256 -c -"):
		return 0
	case strings.HasPrefix(cmd, "curl -fsS"):
		// Health endpoint probe success.
		return 0
	case strings.HasPrefix(cmd, "dnf install") || strings.HasPrefix(cmd, "yum install") || strings.HasPrefix(cmd, "apt-get install"):
		return 0
	case strings.HasPrefix(cmd, "tee /etc/default/minio") || strings.Contains(cmd, "tee /etc/default/minio"):
		return 0
	case strings.Contains(cmd, "systemctl daemon-reload") || strings.HasPrefix(cmd, "systemctl enable"):
		return 0
	case strings.HasPrefix(cmd, "sudo -n bash -c"):
		// Pretend sudo succeeds. The real wrapped command is inside the
		// single-quoted argument; we accept the whole thing.
		return 0
	case strings.Contains(cmd, "systemctl restart") ||
		strings.Contains(cmd, "systemctl stop") ||
		strings.Contains(cmd, "systemctl start"):
		return 0
	case strings.Contains(cmd, "dnf upgrade") ||
		strings.Contains(cmd, "dnf reinstall"):
		return 0
	case strings.Contains(cmd, "shutdown -r") || strings.Contains(cmd, "shutdown -h"):
		// Reboot/shutdown — the real command kills the session. Tests don't
		// actually want the fake server to disappear, so we just return 0.
		return 0
	case cmd == "nproc":
		fmt.Fprintln(stdout, "8")
		return 0
	case cmd == "cat /proc/meminfo":
		fmt.Fprintln(stdout, "MemTotal:       16777216 kB")
		fmt.Fprintln(stdout, "MemFree:         8000000 kB")
		return 0
	case cmd == "cat /etc/os-release":
		fmt.Fprintln(stdout, `NAME="bm-test"`)
		fmt.Fprintln(stdout, `VERSION="1.0"`)
		fmt.Fprintln(stdout, `PRETTY_NAME="bm-test 1.0"`)
		fmt.Fprintln(stdout, `ID=bmtest`)
		return 0
	case strings.HasPrefix(cmd, "lsblk"):
		fmt.Fprintln(stdout, fakeLsblkJSON)
		return 0
	case cmd == "ip -j link":
		fmt.Fprintln(stdout, fakeIPLinkJSON)
		return 0
	case strings.HasPrefix(cmd, "systemctl status buckit"):
		// Pretend buckit isn't installed yet.
		fmt.Fprintln(stderr, "Unit buckit.service could not be found.")
		return 4
	case strings.Contains(cmd, "systemctl show -p LoadState --value buckit.service"),
		strings.Contains(cmd, "systemctl show -p LoadState --value 'buckit.service'"),
		strings.Contains(cmd, "systemctl show -p LoadState --value minio.service"),
		strings.Contains(cmd, "systemctl show -p LoadState --value 'minio.service'"):
		fmt.Fprint(stdout, "loaded")
		return 0
	case strings.HasPrefix(cmd, "systemctl status minio"):
		fmt.Fprintln(stderr, "Unit minio.service could not be found.")
		return 4
	case strings.HasPrefix(cmd, "systemctl is-active buckit"):
		// Used by rollback's pre-flight probe. Default to "active" so the
		// rollback executor proceeds with the per-host revert; tests that
		// need the "already on minio" branch can replace the fake server
		// with one that returns inactive.
		fmt.Fprintln(stdout, "active")
		return 0
	case strings.HasPrefix(cmd, "systemctl is-active minio"):
		fmt.Fprintln(stdout, "inactive")
		return 3
	case strings.HasPrefix(cmd, "echo "):
		fmt.Fprintln(stdout, strings.TrimPrefix(cmd, "echo "))
		return 0
	case strings.HasPrefix(cmd, "sleep "):
		var n int
		fmt.Sscanf(cmd, "sleep %d", &n)
		time.Sleep(time.Duration(n) * time.Second)
		return 0
	case cmd == "fail":
		fmt.Fprintln(stderr, "failed")
		return 7
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", cmd)
		return 127
	}
}

const fakeLsblkJSON = `{
  "blockdevices": [
    {"name":"sda","path":"/dev/sda","size":274877906944,"type":"disk",
     "children":[
        {"name":"sda1","mountpoint":"/boot","fstype":"xfs","type":"part"},
        {"name":"sda2","mountpoint":"/","fstype":"xfs","type":"part"}
     ]},
    {"name":"sdb","path":"/dev/sdb","size":17592186044416,"mountpoint":"/data/disk1","fstype":"xfs","type":"disk"},
    {"name":"sdc","path":"/dev/sdc","size":17592186044416,"mountpoint":"/data/disk2","fstype":"xfs","type":"disk"}
  ]
}`

const fakeIPLinkJSON = `[
  {"ifname":"lo","link_type":"loopback","operstate":"UNKNOWN","address":"00:00:00:00:00:00"},
  {"ifname":"eth0","link_type":"ether","operstate":"UP","address":"00:11:22:33:44:55"}
]`
