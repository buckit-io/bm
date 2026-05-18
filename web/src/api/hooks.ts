// React Query hooks. They currently call the mock API; swapping to a
// real fetch client is a single-file change in mock/api.ts.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "../mock/api";

export function useMe() {
  return useQuery({ queryKey: ["me"], queryFn: api.me });
}

export function useLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ user, pass }: { user: string; pass: string }) =>
      api.login(user, pass),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["me"] }),
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: api.logout,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["me"] }),
  });
}

export function useClusters() {
  return useQuery({ queryKey: ["clusters"], queryFn: api.listClusters });
}

export function useRefreshClusters() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: api.refreshAllClusters,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["clusters"] }),
  });
}

export function useHistory() {
  return useQuery({ queryKey: ["history"], queryFn: api.listHistory });
}

export function useClearHistory() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: api.clearHistory,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["history"] }),
  });
}

export function useCluster(id: string | undefined) {
  return useQuery({
    queryKey: ["cluster", id],
    queryFn: () => api.getCluster(id!),
    enabled: !!id,
  });
}

export function useNodes(clusterId: string | undefined) {
  return useQuery({
    queryKey: ["nodes", clusterId],
    queryFn: () => api.listNodes(clusterId!),
    enabled: !!clusterId,
  });
}

export function useNode(clusterId: string | undefined, nodeId: string | undefined) {
  return useQuery({
    queryKey: ["node", clusterId, nodeId],
    queryFn: () => api.getNode(clusterId!, nodeId!),
    enabled: !!clusterId && !!nodeId,
  });
}

export function useAudit() {
  return useQuery({ queryKey: ["audit"], queryFn: api.listAudit });
}
