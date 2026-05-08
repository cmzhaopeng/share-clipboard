export type Session = {
  authenticated: boolean;
  username?: string;
  isAdmin?: boolean;
  bootstrapAdminUsername?: string;
};

export type UserRecord = {
  username: string;
  isAdmin: boolean;
  createdAt: string;
  updatedAt: string;
};

export type Attachment = {
  name: string;
  storedName: string;
  size: number;
  mimeType: string;
  url: string;
  previewUrl?: string;
};

export type Item = {
  id: string;
  message: string;
  createdAt: string;
  createdBy: string;
  visibility: 'shared' | 'private';
  attachments: Attachment[];
};

async function request<T>(input: RequestInfo | URL, init?: RequestInit): Promise<T> {
  const response = await fetch(input, {
    credentials: 'include',
    ...init,
  });

  if (!response.ok) {
    let message = `请求失败：${response.status}`;
    try {
      const data = await response.json();
      if (typeof data?.error === 'string') {
        message = data.error;
      }
    } catch {
      // ignore
    }
    throw new Error(message);
  }

  if (response.status === 204) {
    return undefined as T;
  }

  return (await response.json()) as T;
}

export function getSession(): Promise<Session> {
  return request<Session>('/api/session');
}

export function login(username: string, password: string): Promise<void> {
  return request<void>('/api/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
}

export function logout(): Promise<void> {
  return request<void>('/api/logout', { method: 'POST' });
}

export function listUsers(): Promise<UserRecord[]> {
  return request<UserRecord[]>('/api/users');
}

export function saveUser(username: string, password: string, isAdmin = false): Promise<void> {
  return request<void>('/api/users', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password, isAdmin }),
  });
}

export function changeOwnPassword(currentPassword: string, newPassword: string): Promise<void> {
  return request<void>('/api/users/change-password', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ currentPassword, newPassword }),
  });
}

export function deleteUser(username: string): Promise<void> {
  return request<void>(`/api/users/${encodeURIComponent(username)}`, {
    method: 'DELETE',
  });
}

export function listItems(): Promise<Item[]> {
  return request<Item[]>('/api/items');
}

export function createItem(message: string, files: File[], visibility: 'shared' | 'private' = 'shared'): Promise<Item> {
  const form = new FormData();
  if (message.trim()) {
    form.set('message', message);
  }
  form.set('visibility', visibility);
  files.forEach((file) => form.append('attachments', file));
  return request<Item>('/api/items', {
    method: 'POST',
    body: form,
  });
}

export function deleteItem(id: string): Promise<void> {
  return request<void>(`/api/items/${id}`, {
    method: 'DELETE',
  });
}
