// These interfaces are view projections, not the complete wire schema.
// Shared keys must follow docs/05 so API changes do not silently break display.

export interface Task {
  id: string;
  title: string;
  repo?: string;
  createdAt?: string;
}

export interface Dispatch {
  id: string;
  taskId: string;
  repo?: string;
  active: boolean;
  createdAt?: string;
}

export interface Evidence {
  taskId: string;
  sha: string;
  boundAt?: string;
}

export interface Ledger {
  tasks?: Task[];
  dispatches?: Dispatch[];
  evidence?: Evidence[];
}
