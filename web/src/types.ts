// Ledger shapes mirror internal/core JSON tags for API responses.
// Ledger mutations are performed by the Go API rather than this module.

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
