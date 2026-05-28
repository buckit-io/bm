package operations

import (
	"testing"

	"github.com/buckit-io/bm/internal/domain"
)

func TestPickConfigSourcePeerWith_PriorityOrder(t *testing.T) {
	target := domain.Node{ID: "t", Hostname: "target", Pool: 2}

	// Catalog of candidate peers across the priority space.
	samePoolOnline := domain.Node{ID: "spo", Hostname: "sp-online", Pool: 2, State: domain.NodeOnline}
	samePoolOffline := domain.Node{ID: "spf", Hostname: "sp-offline", Pool: 2, State: domain.NodeOffline}
	crossPoolOnline := domain.Node{ID: "cpo", Hostname: "cp-online", Pool: 1, State: domain.NodeOnline}
	crossPoolOffline := domain.Node{ID: "cpf", Hostname: "cp-offline", Pool: 1, State: domain.NodeOffline}

	hasMarker := func(set map[string]struct{}) func(domain.Node) bool {
		return func(n domain.Node) bool {
			_, ok := set[n.ID]
			return ok
		}
	}

	cases := []struct {
		name      string
		nodes     []domain.Node
		marker    map[string]struct{}
		wantID    string
		wantFound bool
	}{
		{
			name:      "same-pool online wins over everyone",
			nodes:     []domain.Node{crossPoolOnline, samePoolOffline, samePoolOnline, crossPoolOffline, target},
			marker:    map[string]struct{}{"spo": {}, "spf": {}, "cpo": {}, "cpf": {}},
			wantID:    "spo",
			wantFound: true,
		},
		{
			name:      "same-pool offline beats cross-pool online",
			nodes:     []domain.Node{crossPoolOnline, samePoolOffline, crossPoolOffline, target},
			marker:    map[string]struct{}{"spf": {}, "cpo": {}, "cpf": {}},
			wantID:    "spf",
			wantFound: true,
		},
		{
			name:      "fall back to cross-pool online when no same-pool peer has marker",
			nodes:     []domain.Node{samePoolOnline, samePoolOffline, crossPoolOnline, crossPoolOffline, target},
			marker:    map[string]struct{}{"cpo": {}, "cpf": {}},
			wantID:    "cpo",
			wantFound: true,
		},
		{
			name:      "fall back to cross-pool offline as last resort",
			nodes:     []domain.Node{samePoolOnline, crossPoolOffline, target},
			marker:    map[string]struct{}{"cpf": {}},
			wantID:    "cpf",
			wantFound: true,
		},
		{
			name:      "no peer has marker → not found",
			nodes:     []domain.Node{samePoolOnline, crossPoolOnline, target},
			marker:    map[string]struct{}{},
			wantID:    "",
			wantFound: false,
		},
		{
			name:      "target itself is skipped even if it has marker",
			nodes:     []domain.Node{target},
			marker:    map[string]struct{}{"t": {}},
			wantID:    "",
			wantFound: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peer, ok := pickConfigSourcePeerWith(tc.nodes, target, hasMarker(tc.marker))
			if ok != tc.wantFound {
				t.Fatalf("found=%v, want %v", ok, tc.wantFound)
			}
			if peer.ID != tc.wantID {
				t.Fatalf("got peer ID %q, want %q", peer.ID, tc.wantID)
			}
		})
	}
}
