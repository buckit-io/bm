package admin

import (
	"context"
	"encoding/json"
	"errors"

	madmin "github.com/buckit-io/madmin-go/v3"

	"github.com/buckit-io/bm/internal/domain"
)

// ListUsers returns every long-term IAM user the admin API exposes. The
// secret keys are NOT included — they cannot be fetched after creation.
// Operators rotate creds during migration.
func (c *Client) ListUsers(ctx context.Context) ([]domain.UserSnapshot, error) {
	if c == nil || c.adm == nil {
		return nil, errors.New("admin: nil client")
	}
	users, err := c.adm.ListUsers(ctx)
	if err != nil {
		return nil, classifyError(err)
	}
	out := make([]domain.UserSnapshot, 0, len(users))
	for ak, info := range users {
		policies := splitCSV(info.PolicyName)
		out = append(out, domain.UserSnapshot{
			AccessKey: ak,
			Status:    string(info.Status),
			Policies:  policies,
		})
	}
	return out, nil
}

// ListGroups returns every IAM group + member list.
func (c *Client) ListGroups(ctx context.Context) ([]domain.GroupSnapshot, error) {
	if c == nil || c.adm == nil {
		return nil, errors.New("admin: nil client")
	}
	names, err := c.adm.ListGroups(ctx)
	if err != nil {
		return nil, classifyError(err)
	}
	out := make([]domain.GroupSnapshot, 0, len(names))
	for _, n := range names {
		desc, derr := c.adm.GetGroupDescription(ctx, n)
		if derr != nil {
			// Soft-fail per group so one missing group doesn't block the
			// whole snapshot. The caller logs Warnings.
			out = append(out, domain.GroupSnapshot{Name: n})
			continue
		}
		out = append(out, domain.GroupSnapshot{
			Name:    desc.Name,
			Status:  desc.Status,
			Members: desc.Members,
			Policy:  desc.Policy,
		})
	}
	return out, nil
}

// ListCannedPolicies returns every IAM policy by name + JSON body.
func (c *Client) ListCannedPolicies(ctx context.Context) ([]domain.PolicySnapshot, error) {
	if c == nil || c.adm == nil {
		return nil, errors.New("admin: nil client")
	}
	policies, err := c.adm.ListCannedPolicies(ctx)
	if err != nil {
		return nil, classifyError(err)
	}
	out := make([]domain.PolicySnapshot, 0, len(policies))
	for name, body := range policies {
		out = append(out, domain.PolicySnapshot{
			Name:   name,
			Policy: string(body),
		})
	}
	return out, nil
}

// ListServiceAccounts returns every minio service account, scoped across all
// users. Walking per-user is necessary because the admin API only lists
// per-parent.
func (c *Client) ListServiceAccounts(ctx context.Context, users []domain.UserSnapshot) ([]domain.ServiceAccountSnapshot, error) {
	if c == nil || c.adm == nil {
		return nil, errors.New("admin: nil client")
	}
	out := make([]domain.ServiceAccountSnapshot, 0, len(users))
	seen := make(map[string]struct{})
	collect := func(parent string) {
		resp, err := c.adm.ListServiceAccounts(ctx, parent)
		if err != nil {
			return
		}
		for _, sa := range resp.Accounts {
			if _, ok := seen[sa.AccessKey]; ok {
				continue
			}
			seen[sa.AccessKey] = struct{}{}
			out = append(out, domain.ServiceAccountSnapshot{
				AccessKey:   sa.AccessKey,
				ParentUser:  sa.ParentUser,
				Status:      sa.AccountStatus,
				Name:        sa.Name,
				Description: sa.Description,
			})
		}
	}
	// "" → service accounts owned by the requesting (root) user.
	collect("")
	for _, u := range users {
		collect(u.AccessKey)
	}
	return out, nil
}

// splitCSV splits madmin's comma-joined PolicyName into a clean list. madmin
// joins multiple attached policies with "," and no spaces.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			tok := s[start:i]
			start = i + 1
			if tok != "" {
				out = append(out, tok)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// _ keeps json imported for future inline use of policy bodies.
var _ = json.RawMessage(nil)

// _ keeps madmin imported even when this file is the only reader.
var _ = madmin.AccountEnabled
