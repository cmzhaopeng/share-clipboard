import { useEffect, useMemo, useRef, useState } from 'react';
import {
  changeOwnPassword,
  createItem,
  deleteItem,
  deleteUser,
  getSession,
  listItems,
  listUsers,
  login,
  logout,
  saveUser,
  type Item,
  type UserRecord,
} from './api';

type PendingFile = {
  id: string;
  file: File;
  previewUrl?: string;
};

function App() {
  const [sessionChecked, setSessionChecked] = useState(false);
  const [authenticated, setAuthenticated] = useState(false);
  const [currentUser, setCurrentUser] = useState('');
  const [isAdmin, setIsAdmin] = useState(false);
  const [bootstrapAdminUsername, setBootstrapAdminUsername] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [items, setItems] = useState<Item[]>([]);
  const [users, setUsers] = useState<UserRecord[]>([]);
  const [newUsername, setNewUsername] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [newIsAdmin, setNewIsAdmin] = useState(false);
  const [currentPassword, setCurrentPassword] = useState('');
  const [nextPassword, setNextPassword] = useState('');
  const [message, setMessage] = useState('');
  const [itemVisibility, setItemVisibility] = useState<'shared' | 'private'>('shared');
  const [pendingFiles, setPendingFiles] = useState<PendingFile[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copyState, setCopyState] = useState<Record<string, string>>({});
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const pendingFilesRef = useRef<PendingFile[]>([]);

  useEffect(() => {
    void bootstrap();
  }, []);

  useEffect(() => {
    pendingFilesRef.current = pendingFiles;
  }, [pendingFiles]);

  useEffect(() => {
    return () => {
      pendingFilesRef.current.forEach((file) => file.previewUrl && URL.revokeObjectURL(file.previewUrl));
    };
  }, []);

  async function bootstrap() {
    try {
      const session = await getSession();
      setAuthenticated(session.authenticated);
      setCurrentUser(session.username ?? '');
      setIsAdmin(Boolean(session.isAdmin));
      setBootstrapAdminUsername(session.bootstrapAdminUsername ?? '');
      if (session.authenticated) {
        await refreshItems();
        if (session.isAdmin) {
          await refreshUsers();
        } else {
          setUsers([]);
        }
      }
    } catch {
      setAuthenticated(false);
      setCurrentUser('');
      setIsAdmin(false);
      setBootstrapAdminUsername('');
    } finally {
      setSessionChecked(true);
    }
  }

  async function refreshItems() {
    const data = await listItems();
    setItems(data);
  }

  async function refreshUsers() {
    const data = await listUsers();
    setUsers(data);
  }

  async function onLogin(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await login(username, password);
      const session = await getSession();
      setPassword('');
      setAuthenticated(true);
      setCurrentUser(session.username ?? username);
      setIsAdmin(Boolean(session.isAdmin));
      setBootstrapAdminUsername(session.bootstrapAdminUsername ?? '');
      await refreshItems();
      if (session.isAdmin) {
        await refreshUsers();
      } else {
        setUsers([]);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败');
    } finally {
      setBusy(false);
      setSessionChecked(true);
    }
  }

  async function onLogout() {
    setBusy(true);
    setError(null);
    try {
      await logout();
      setAuthenticated(false);
      setCurrentUser('');
      setIsAdmin(false);
      setBootstrapAdminUsername('');
      setItems([]);
      setUsers([]);
    } catch (err) {
      setError(err instanceof Error ? err.message : '退出失败');
    } finally {
      setBusy(false);
    }
  }

  function addFiles(files: FileList | File[]) {
    const incoming = Array.from(files);
    if (incoming.length === 0) return;
    const mapped = incoming.map((file) => ({
      id: `${file.name}-${file.size}-${crypto.randomUUID()}`,
      file,
      previewUrl: file.type.startsWith('image/') ? URL.createObjectURL(file) : undefined,
    }));
    setPendingFiles((current) => [...current, ...mapped]);
  }

  function removePendingFile(id: string) {
    setPendingFiles((current) => {
      const target = current.find((entry) => entry.id === id);
      if (target?.previewUrl) URL.revokeObjectURL(target.previewUrl);
      return current.filter((entry) => entry.id !== id);
    });
  }

  async function submitItem(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError(null);
    if (!message.trim() && pendingFiles.length === 0) {
      setError('请输入消息或添加附件');
      return;
    }
    setBusy(true);
    try {
      const created = await createItem(message, pendingFiles.map((entry) => entry.file), itemVisibility);
      setItems((current) => [created, ...current]);
      setMessage('');
      setItemVisibility('shared');
      pendingFiles.forEach((entry) => entry.previewUrl && URL.revokeObjectURL(entry.previewUrl));
      setPendingFiles([]);
      textareaRef.current?.focus();
    } catch (err) {
      setError(err instanceof Error ? err.message : '提交失败');
    } finally {
      setBusy(false);
    }
  }

  async function saveUserAccount(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError(null);
    if (!newUsername.trim() || !newPassword.trim()) {
      setError('请输入用户名和密码');
      return;
    }
    setBusy(true);
    try {
      await saveUser(newUsername.trim(), newPassword, newIsAdmin);
      setNewUsername('');
      setNewPassword('');
      setNewIsAdmin(false);
      await refreshUsers();
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存用户失败');
    } finally {
      setBusy(false);
    }
  }

  async function submitPasswordChange(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError(null);
    if (!currentPassword.trim() || !nextPassword.trim()) {
      setError('请输入当前密码和新密码');
      return;
    }
    setBusy(true);
    try {
      await changeOwnPassword(currentPassword, nextPassword);
      setCurrentPassword('');
      setNextPassword('');
      setAuthenticated(false);
      setCurrentUser('');
      setIsAdmin(false);
      setBootstrapAdminUsername('');
      setItems([]);
      setUsers([]);
      setError('密码修改成功，请使用新密码重新登录');
    } catch (err) {
      setError(err instanceof Error ? err.message : '修改密码失败');
    } finally {
      setBusy(false);
    }
  }

  async function removeManagedUser(username: string) {
    if (!window.confirm(`删除用户 ${username} 后，该账号及其会话将立即失效，确认删除？`)) return;
    setBusy(true);
    setError(null);
    try {
      await deleteUser(username);
      setUsers((current) => current.filter((user) => user.username !== username));
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除用户失败');
    } finally {
      setBusy(false);
    }
  }

  async function removeItem(id: string) {
    if (!window.confirm('删除后服务器也不会保留备份，确认删除？')) return;
    setBusy(true);
    setError(null);
    try {
      await deleteItem(id);
      setItems((current) => current.filter((item) => item.id !== id));
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败');
    } finally {
      setBusy(false);
    }
  }

  async function copyMessage(item: Item) {
    try {
      await navigator.clipboard.writeText(item.message);
      setCopyState((current) => ({ ...current, [item.id]: '已复制' }));
      window.setTimeout(() => {
        setCopyState((current) => {
          const next = { ...current };
          delete next[item.id];
          return next;
        });
      }, 1500);
    } catch {
      setCopyState((current) => ({ ...current, [item.id]: '复制失败' }));
    }
  }

  function visibilityLabel(item: Item): string {
    return item.visibility === 'private' ? '仅自己可见' : '所有用户可见';
  }

  function visibilityClassName(item: Item): string {
    return item.visibility === 'private' ? 'visibility-private' : 'visibility-shared';
  }

  const emptyText = useMemo(() => '暂无内容，可粘贴文字、截图或上传多个附件。', []);
  const sharedItems = useMemo(() => items.filter((item) => item.visibility === 'shared'), [items]);
  const privateItems = useMemo(() => items.filter((item) => item.visibility === 'private'), [items]);

  if (!sessionChecked) {
    return <div className="center-panel">正在检查登录状态…</div>;
  }

  if (!authenticated) {
    return (
      <div className="page login-page">
        <div className="card login-card">
          <h1>Shared Clipboard</h1>
          <p>轻量级远程共享剪贴板，支持消息、图片和附件。</p>
          <form onSubmit={onLogin} className="stack gap-16">
            <label>
              <span>用户名</span>
              <input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" required />
            </label>
            <label>
              <span>密码</span>
              <input value={password} onChange={(e) => setPassword(e.target.value)} type="password" autoComplete="current-password" required />
            </label>
            {error && <div className="error">{error}</div>}
            <button type="submit" disabled={busy}>{busy ? '登录中…' : '登录'}</button>
          </form>
        </div>
      </div>
    );
  }

  return (
    <div className="page">
      <header className="topbar">
        <div>
          <h1>Shared Clipboard</h1>
          <p>当前登录：{currentUser || '未知用户'}。支持多用户账号管理、SQLite 持久化与跨终端共享。</p>
        </div>
        <div className="topbar-actions">
          <form className="topbar-password-form" onSubmit={submitPasswordChange}>
            <input
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              type="password"
              autoComplete="current-password"
              placeholder="当前密码"
            />
            <input
              value={nextPassword}
              onChange={(e) => setNextPassword(e.target.value)}
              type="password"
              autoComplete="new-password"
              placeholder="新密码"
            />
            <button type="submit" className="secondary" disabled={busy}>修改我的密码</button>
          </form>
          <button className="secondary" onClick={onLogout} disabled={busy}>退出登录</button>
        </div>
      </header>

      <main className="layout three-column">
        <section className="card composer-card">
          <form onSubmit={submitItem} className="stack gap-16">
            <textarea
              ref={textareaRef}
              value={message}
              onChange={(e) => setMessage(e.target.value)}
              rows={6}
              placeholder="可直接粘贴文字、截图，或拖入多个附件。"
              onPaste={(event) => {
                const files = Array.from(event.clipboardData.files);
                if (files.length > 0) addFiles(files);
              }}
            />
            <div className="visibility-toggle" role="group" aria-label="剪贴板可见范围">
              <button
                type="button"
                className={itemVisibility === 'private' ? 'secondary active' : 'secondary'}
                onClick={() => setItemVisibility('private')}
              >
                自己共享
              </button>
              <button
                type="button"
                className={itemVisibility === 'shared' ? 'secondary active' : 'secondary'}
                onClick={() => setItemVisibility('shared')}
              >
                所有用户可见
              </button>
            </div>
            <div className="hint visibility-hint">
              当前发送范围：{itemVisibility === 'private' ? '仅自己可见，管理员可查看' : '所有普通用户可见，管理员也可查看'}
            </div>
            <div
              className="dropzone"
              onDragOver={(event) => {
                event.preventDefault();
                event.dataTransfer.dropEffect = 'copy';
              }}
              onDrop={(event) => {
                event.preventDefault();
                addFiles(event.dataTransfer.files);
              }}
            >
              <div>拖拽文件到此处，或</div>
              <button type="button" className="secondary" onClick={() => fileInputRef.current?.click()}>选择附件</button>
              <input
                ref={fileInputRef}
                type="file"
                multiple
                hidden
                onChange={(event) => {
                  if (event.target.files) {
                    addFiles(event.target.files);
                    event.target.value = '';
                  }
                }}
              />
            </div>
            {pendingFiles.length > 0 && (
              <div className="pending-grid">
                {pendingFiles.map((entry) => (
                  <div key={entry.id} className="pending-item">
                    {entry.previewUrl ? <img src={entry.previewUrl} alt={entry.file.name} /> : <div className="file-chip">{entry.file.name}</div>}
                    <div className="pending-meta">
                      <strong>{entry.file.name}</strong>
                      <span>{formatSize(entry.file.size)}</span>
                    </div>
                    <button type="button" className="danger ghost" onClick={() => removePendingFile(entry.id)}>移除</button>
                  </div>
                ))}
              </div>
            )}
            {error && <div className="error">{error}</div>}
            <div className="actions">
              <span className="hint">删除即从服务器移除，不保留历史备份。</span>
              <button type="submit" disabled={busy}>{busy ? '处理中…' : '发送到共享剪贴板'}</button>
            </div>
          </form>
        </section>

        <section className="card list-card">
          <div className="list-header">
            <h2>最近内容</h2>
            <button className="secondary" onClick={() => void refreshItems()} disabled={busy}>刷新</button>
          </div>
          {items.length === 0 ? (
            <div className="empty">{emptyText}</div>
          ) : (
            <div className="item-groups">
              <section className="item-group">
                <div className="group-header">
                  <h3>所有用户可见</h3>
                  <span className="hint">{sharedItems.length} 条</span>
                </div>
                {sharedItems.length === 0 ? (
                  <div className="empty compact-empty">暂无共享内容</div>
                ) : (
                  <div className="item-list">
                    {sharedItems.map((item) => (
                      <article key={item.id} className="item-card">
                        <div className="item-header multi-line">
                          <div>
                            <time>{new Date(item.createdAt).toLocaleString()}</time>
                            <div className="hint">发布用户：{item.createdBy || '未知用户'}</div>
                          </div>
                          <div className="inline-actions item-header-actions">
                            <span className={`visibility-badge ${visibilityClassName(item)}`}>{visibilityLabel(item)}</span>
                            {item.message && (
                              <button className="secondary ghost" onClick={() => void copyMessage(item)} type="button">
                                {copyState[item.id] || '复制文本'}
                              </button>
                            )}
                            <button className="danger ghost" onClick={() => void removeItem(item.id)} disabled={busy}>删除</button>
                          </div>
                        </div>
                        {item.message && <pre className="message-block">{item.message}</pre>}
                        {item.attachments.length > 0 && (
                          <div className="attachment-grid">
                            {item.attachments.map((attachment) => (
                              <a key={attachment.storedName} className="attachment-card" href={attachment.url} target="_blank" rel="noreferrer">
                                {attachment.previewUrl ? <img src={attachment.previewUrl} alt={attachment.name} /> : <div className="file-chip">{attachment.name}</div>}
                                <div className="pending-meta">
                                  <strong>{attachment.name}</strong>
                                  <span>{formatSize(attachment.size)}</span>
                                </div>
                              </a>
                            ))}
                          </div>
                        )}
                      </article>
                    ))}
                  </div>
                )}
              </section>

              <section className="item-group">
                <div className="group-header">
                  <h3>{isAdmin ? '用户独享内容（管理员可见）' : '自己共享内容'}</h3>
                  <span className="hint">{privateItems.length} 条</span>
                </div>
                {privateItems.length === 0 ? (
                  <div className="empty compact-empty">暂无独享内容</div>
                ) : (
                  <div className="item-list">
                    {privateItems.map((item) => (
                      <article key={item.id} className="item-card private-item-card">
                        <div className="item-header multi-line">
                          <div>
                            <time>{new Date(item.createdAt).toLocaleString()}</time>
                            <div className="hint">发布用户：{item.createdBy || '未知用户'}</div>
                          </div>
                          <div className="inline-actions item-header-actions">
                            <span className={`visibility-badge ${visibilityClassName(item)}`}>{visibilityLabel(item)}</span>
                            {item.message && (
                              <button className="secondary ghost" onClick={() => void copyMessage(item)} type="button">
                                {copyState[item.id] || '复制文本'}
                              </button>
                            )}
                            <button className="danger ghost" onClick={() => void removeItem(item.id)} disabled={busy}>删除</button>
                          </div>
                        </div>
                        {item.message && <pre className="message-block">{item.message}</pre>}
                        {item.attachments.length > 0 && (
                          <div className="attachment-grid">
                            {item.attachments.map((attachment) => (
                              <a key={attachment.storedName} className="attachment-card" href={attachment.url} target="_blank" rel="noreferrer">
                                {attachment.previewUrl ? <img src={attachment.previewUrl} alt={attachment.name} /> : <div className="file-chip">{attachment.name}</div>}
                                <div className="pending-meta">
                                  <strong>{attachment.name}</strong>
                                  <span>{formatSize(attachment.size)}</span>
                                </div>
                              </a>
                            ))}
                          </div>
                        )}
                      </article>
                    ))}
                  </div>
                )}
              </section>
            </div>
          )}
        </section>

        {isAdmin && (
          <section className="card users-card">
            <div className="list-header">
              <h2>用户管理</h2>
              <button className="secondary" onClick={() => void refreshUsers()} disabled={busy}>刷新</button>
            </div>
            <form onSubmit={saveUserAccount} className="stack gap-16">
              <label>
                <span>新增/重置用户名</span>
                <input value={newUsername} onChange={(e) => setNewUsername(e.target.value)} placeholder="例如：alice" />
              </label>
              <label>
                <span>设置密码</span>
                <input value={newPassword} onChange={(e) => setNewPassword(e.target.value)} type="password" placeholder="输入新密码" />
              </label>
              <label className="checkbox-row">
                <input type="checkbox" checked={newIsAdmin} onChange={(e) => setNewIsAdmin(e.target.checked)} />
                <span>设为管理员</span>
              </label>
              <button type="submit" disabled={busy}>{busy ? '保存中…' : '保存用户'}</button>
            </form>
            <div className="user-list">
              {users.map((user) => (
                <div key={user.username} className="user-row managed-user-row">
                  <div className="managed-user-meta">
                    <strong>{user.username}</strong>
                    <span>{user.isAdmin ? '管理员' : '普通用户'} · 更新于：{new Date(user.updatedAt).toLocaleString()}</span>
                  </div>
                  <div className="inline-actions">
                    <button
                      type="button"
                      className="danger ghost"
                      onClick={() => void removeManagedUser(user.username)}
                      disabled={busy || user.username === currentUser || user.username === bootstrapAdminUsername}
                      title={
                        user.username === currentUser
                          ? '当前登录管理员不可在此处删除'
                          : user.username === bootstrapAdminUsername
                            ? '系统引导管理员不可删除'
                            : '删除该用户'
                      }
                    >
                      删除用户
                    </button>
                  </div>
                </div>
              ))}
            </div>
          </section>
        )}
      </main>
    </div>
  );
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

export default App;
