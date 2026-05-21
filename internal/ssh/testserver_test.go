package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// testServer is a minimal in-process SSH server backed by x/crypto/ssh. It
// accepts a known username + password and runs a tiny hand-rolled command
// table — enough to drive Run / RunStream / Probe tests without Docker.
type testServer struct {
	t        *testing.T
	listener net.Listener
	addr     string
	hostKey  gossh.Signer
	user     string
	password string

	wg     sync.WaitGroup
	stopCh chan struct{}
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &testServer{
		t:        t,
		listener: ln,
		addr:     ln.Addr().String(),
		hostKey:  signer,
		user:     "tester",
		password: "hunter2",
		stopCh:   make(chan struct{}),
	}
	srv.wg.Add(1)
	go srv.serve()
	t.Cleanup(srv.Stop)
	return srv
}

func (s *testServer) Stop() {
	select {
	case <-s.stopCh:
		return
	default:
		close(s.stopCh)
	}
	_ = s.listener.Close()
	s.wg.Wait()
}

func (s *testServer) HostPort() (string, int) {
	host, portStr, err := net.SplitHostPort(s.addr)
	if err != nil {
		s.t.Fatal(err)
	}
	var p int
	fmt.Sscanf(portStr, "%d", &p)
	return host, p
}

func (s *testServer) serve() {
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

func (s *testServer) handleConn(c net.Conn, cfg *gossh.ServerConfig) {
	defer s.wg.Done()
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

func (s *testServer) handleSession(ch gossh.Channel, reqs <-chan *gossh.Request) {
	defer s.wg.Done()
	defer ch.Close()
	for req := range reqs {
		switch req.Type {
		case "exec":
			cmd := parseExecPayload(req.Payload)
			req.Reply(true, nil)
			exit := s.runFakeCommand(cmd, ch, ch.Stderr())
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

// runFakeCommand simulates a few commands tests need:
//   echo X        — print X to stdout, exit 0
//   uname -a      — fixed kernel string, exit 0
//   hostname      — fixed hostname, exit 0
//   cat /etc/os-release — fixed os-release blob, exit 0
//   stream-N      — emit N stdout lines + 1 stderr line, exit 0
//   sleep N       — block N seconds, then exit 0 (used by cancel tests)
//   fail          — exit 7
func (s *testServer) runFakeCommand(cmd string, stdout io.Writer, stderr io.Writer) int {
	switch {
	case cmd == "uname -a":
		fmt.Fprintln(stdout, "Linux fake 6.6.0-test #1 SMP x86_64 GNU/Linux")
		return 0
	case cmd == "hostname":
		fmt.Fprintln(stdout, "fakehost")
		return 0
	case cmd == "cat /etc/os-release":
		fmt.Fprintln(stdout, `NAME="bm-test"`)
		fmt.Fprintln(stdout, `VERSION="1.0"`)
		fmt.Fprintln(stdout, `ID=bmtest`)
		return 0
	case strings.HasPrefix(cmd, "echo "):
		fmt.Fprintln(stdout, strings.TrimPrefix(cmd, "echo "))
		return 0
	case strings.HasPrefix(cmd, "stream-"):
		var n int
		fmt.Sscanf(cmd, "stream-%d", &n)
		for i := 0; i < n; i++ {
			fmt.Fprintf(stdout, "line-%d\n", i)
		}
		fmt.Fprintln(stderr, "warning: stream done")
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
