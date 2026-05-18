// Operation catalog — every Cluster Actions menu item is one entry
// here. The OperationModal consumes these defs to render input,
// dispatch, and terminal phases consistently.
//
// Flavors:
//   signal       — admin API one-shot. Dispatch IS the op. Modal flips
//                  to terminal phase immediately on success.
//   orchestrated — bm runs a multi-step per-node loop. Modal stays in
//                  running phase until bm reports terminal.
//   navigate     — not really an operation. The menu item just routes
//                  to a different page. Handled inline by the menu.

import { ReactNode } from "react";
import { Cluster } from "../../../mock/data";

export type OperationFlavor = "signal" | "orchestrated" | "navigate";
// Free-form group key — the menu's caller supplies a Record<group,label>
// at render time. Cluster Actions uses admin/ssh/manager; the per-node
// Actions menu uses service/software/diagnostics/host.
export type OperationGroup = string;

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export interface InputStep<P = any> {
  id: string;
  render: (props: {
    params: P;
    setParams: (next: P) => void;
    cluster: Cluster;
  }) => ReactNode;
  canAdvance: (params: P, cluster: Cluster) => boolean;
  // Override the default "Continue →" label for the final step
  // (e.g., "Rotate", "Remove", "Start upgrade").
  nextLabel?: string;
  // Tone for the final-step Continue button.
  danger?: boolean;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export interface OperationDef<P = any> {
  id: string;
  group: OperationGroup;
  label: string;
  // One-liner explaining what the operator is about to do.
  description: string;
  flavor: OperationFlavor;
  // Optional dynamic label override (e.g., Freeze ↔ Unfreeze depending
  // on cluster.apiFrozen). Falls back to `label`.
  dynamicLabel?: (cluster: Cluster) => string;
  // Should this item be hidden for the given cluster? Used by
  // Unfreeze to hide when the cluster isn't frozen.
  visible?: (cluster: Cluster) => boolean;
  // Marks the item as destructive — danger styling in the menu.
  danger?: boolean;
  // Whether the running phase exposes a Cancel button. Signal ops are
  // never cancellable (the dispatch IS the op). Orchestrated ops that
  // leave the cluster in a recoverable state when stopped mid-flight
  // can opt in; mixed-state-unsafe ops (rolling upgrade, rotate root
  // creds) should leave this false.
  cancellable?: boolean;
  // Override for the running-phase Cancel button label. Defaults to
  // "Cancel after current host". Heal-style follow ops use this to say
  // "Stop following" since canceling doesn't actually stop the op.
  cancelLabel?: string;
  // Input steps run before dispatch. May be empty (no confirmation
  // needed) — but in practice every op gets at least a one-line
  // confirmation step.
  inputSteps: InputStep<P>[];
  initialParams: P;
  // Returns the OpKind for mock/api.ts dispatchOperation.
  opKind?: string;
  // For navigate flavor only. Receives the cluster plus an optional
  // context bag — used by per-node menu items to pass the node id.
  navigateTo?: (cluster: Cluster, ctx?: NavigateContext) => string;
}

export interface NavigateContext {
  nodeId?: string;
}
