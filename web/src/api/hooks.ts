// React Query hooks against the real /api/v1 backend.
//
// Hook signatures + queryKeys are stable so pages don't change beyond the
// import path swap. Mutations expose the same shape mock/api.ts did
// (LoginResult etc.) so consuming pages (Login.tsx) stay byte-stable.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as client from "./client";
import { ApiError } from "./client";
import type {
  Cluster,
  ClusterSshConfig,
  HistoryEntry,
  Node,
  SessionMe,
} from "./types";

// ---- session ----

export function useMe() {
  return useQuery<SessionMe | null>({
    queryKey: ["me"],
    queryFn: async ({ signal }) => {
      try {
        return await client.me(signal);
      } catch (err) {
        // 401/403 → not signed in. Returning null matches the mock's
        // unauthenticated case so AppShell's existing guard logic still works.
        if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
          return null;
        }
        throw err;
      }
    },
  });
}

// LoginResult mirrors mock/api.ts so Login.tsx's onSuccess handler stays
// stable. The remote-access endpoint is M-remote-access (501 today); a
// real failure surfaces as `{ok:false, error}` rather than throwing.
export type LoginResult = { ok: true } | { ok: false; error: string };

export function useLogin() {
  const qc = useQueryClient();
  return useMutation<LoginResult, unknown, { user: string; pass: string }>({
    mutationFn: async ({ user, pass }) => {
      try {
        await fetch("/api/v1/sessions/login", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ user, pass }),
          credentials: "same-origin",
        }).then(async (r) => {
          if (!r.ok) {
            const text = await r.text();
            try {
              const env = JSON.parse(text) as { message?: string };
              throw new Error(env.message ?? text ?? r.statusText);
            } catch {
              throw new Error(text || r.statusText);
            }
          }
        });
        return { ok: true };
      } catch (err) {
        const msg = err instanceof Error ? err.message : "login failed";
        return { ok: false, error: msg };
      }
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["me"] }),
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      await fetch("/api/v1/sessions/logout", {
        method: "POST",
        credentials: "same-origin",
      });
      return { ok: true } as const;
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["me"] }),
  });
}

// ---- clusters ----

export function useClusters() {
  return useQuery<Cluster[]>({
    queryKey: ["clusters"],
    queryFn: ({ signal }) => client.listClusters(signal),
  });
}

export function useRefreshClusters() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => client.refreshAllClusters(),
    onSuccess: (clusters) => {
      qc.setQueryData(["clusters"], clusters);
      qc.invalidateQueries({ queryKey: ["clusters"] });
    },
  });
}

export function useRefreshCluster(id: string | undefined) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => client.refreshOneCluster(id!),
    onSuccess: (cluster) => {
      qc.setQueryData(["cluster", id], cluster);
      qc.invalidateQueries({ queryKey: ["clusters"] });
      qc.invalidateQueries({ queryKey: ["nodes", id] });
    },
  });
}

export function useCluster(id: string | undefined) {
  return useQuery<Cluster | null>({
    queryKey: ["cluster", id],
    queryFn: ({ signal }) => client.getCluster(id!, signal),
    enabled: !!id,
  });
}

export function useClusterSshConfig(clusterId: string | undefined) {
  return useQuery<ClusterSshConfig | null>({
    queryKey: ["cluster-ssh", clusterId],
    queryFn: ({ signal }) => client.getClusterSshConfig(clusterId!, signal),
    enabled: !!clusterId,
  });
}

// ---- nodes ----

export function useNodes(clusterId: string | undefined) {
  return useQuery<Node[]>({
    queryKey: ["nodes", clusterId],
    queryFn: ({ signal }) => client.listNodes(clusterId!, signal),
    enabled: !!clusterId,
  });
}

export function useNode(
  clusterId: string | undefined,
  nodeId: string | undefined,
) {
  return useQuery<Node | null>({
    queryKey: ["node", clusterId, nodeId],
    queryFn: ({ signal }) => client.getNode(clusterId!, nodeId!, signal),
    enabled: !!clusterId && !!nodeId,
  });
}

// ---- history ----

export function useHistory() {
  return useQuery<HistoryEntry[]>({
    queryKey: ["history"],
    queryFn: ({ signal }) => client.listHistory(signal),
  });
}

export function useClearHistory() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => client.clearHistory(),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["history"] }),
  });
}
