package config

// Settings is the operator's persistent preferences for `bm` itself —
// remote-access state, optional version pin, etc. Loaded from / saved to the
// `settings/app` bbolt bucket; defaults applied here.
type Settings struct {
	RemoteAccess RemoteAccess `json:"remoteAccess"`
	VersionPin   string       `json:"versionPin,omitempty"`
}

// RemoteAccess controls the optional non-loopback listener.
type RemoteAccess struct {
	Enabled   bool   `json:"enabled"`
	Passcode  string `json:"-"` // never serialise; lives in encrypted bucket
	TLSCert   string `json:"tlsCert,omitempty"`
	TLSKey    string `json:"tlsKey,omitempty"`
}

// DefaultSettings returns the zero-config preference set the operator gets on
// first launch: loopback only, no version pin.
func DefaultSettings() Settings {
	return Settings{
		RemoteAccess: RemoteAccess{Enabled: false},
	}
}
