// Ephemeral history state used only for in-app cluster/node navigation.
// Consumers must clear one-time flags after handling them.

export type ClusterDetailNavigationState = {
  skipInitialRefresh?: boolean;
};

export type NodeDetailNavigationState = {
  fromClusterDetail?: boolean;
};
