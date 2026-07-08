package admin

import (
	madmin "github.com/buckit-io/madmin-go/v3"
	"github.com/buckit-io/buckit-go/v7/pkg/credentials"
)

// credsForOptions builds the credentials.Credentials object madmin.Options
// wants. Static V4 — bm never uses STS/AssumeRole; the operator supplies the
// admin access/secret directly.
func credsForOptions(accessKey, secretKey string) *credentials.Credentials {
	return credentials.NewStaticV4(accessKey, secretKey, "")
}

// Keep the madmin import referenced even when ServerInfo/AccountInfo are not
// compiled in (e.g. via build tags later). Cheap no-op.
var _ = madmin.NewWithOptions
