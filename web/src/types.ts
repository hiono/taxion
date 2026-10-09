// Ledger shapes mirror internal/core JSON tags. Read-only: the web slice
// never constructs mutations, it only renders what the CLI wrote.

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
