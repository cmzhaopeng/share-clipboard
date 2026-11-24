import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  changeOwnPassword,
  createItem,
  deleteItem,
  updateItemVisibility,
  deleteUser,
  getSession,
  listItems,
  listUsers,
  login,
  logout,
  saveUser,
  type Attachment,
  type Item,
  type UserRecord,
} from './api';
import ApiTokensPanel from './ApiTokensPanel';
import AttachmentViewer, { fileExtension } from './AttachmentViewer';
import { attachmentKind, copyAttachment, copyKind, shareAttachment, shareLabel } from './fileActions';
import { formatSize, formatTime } from './format';

type PendingFile = {
  id: string;
  file: File;
  previewUrl?: string;
};

type Tab = 'shared' | 'private';

const COLLAPSE_CHARS = 280;

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
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>('shared');
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [viewing, setViewing] = useState<Attachment | null>(null);
  const [fileBusy, setFileBusy] = useState<Record<string, boolean>>({});
  const [live, setLive] = useState(false);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const pendingFilesRef = useRef<PendingFile[]>([]);
  const toastTimer = useRef<number | undefined>(undefined);

  const showToast = useCallback((text: string) => {
    setToast(text);
    window.clearTimeout(toastTimer.current);
    toastTimer.current = window.setTimeout(() => setToast(null), 2000);
  }, []);

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

  // Home-screen web apps have no pull-to-refresh and are often resumed from the background:
  // reload the list whenever the app comes back to the foreground.
  useEffect(() => {
    if (!authenticated) return;
    const onVisible = () => {
      if (document.visibilityState === 'visible') void refreshItems(true);
    };
    document.addEventListener('visibilitychange', onVisible);
    window.addEventListener('pageshow', onVisible);
    return () => {
      document.removeEventListener('visibilitychange', onVisible);
      window.removeEventListener('pageshow', onVisible);
    };
  }, [authenticated]);

  // Live updates: the server pushes a notice whenever an item visible to us is added or removed,
  // and we reload the list. EventSource reconnects by itself; on (re)connect we resync.
  useEffect(() => {
    if (!authenticated || typeof EventSource === 'undefined') return;
    const source = new EventSource('/api/events');
    let timer: number | undefined;
    const reload = () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => void refreshItems(true), 150);
    };
    source.onopen = () => {
      setLive(true);
      reload();
    };
    source.onerror = () => setLive(false);
    source.addEventListener('item', reload);
    return () => {
      window.clearTimeout(timer);
      source.close();
      setLive(false);
    };
  }, [authenticated]);

  // Grow the textarea with its content instead of scrolling inside a tiny box.
  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${Math.min(el.scrollHeight, window.innerHeight * 0.45)}px`;
  }, [message]);

  function resetSession() {
    setAuthenticated(false);
    setCurrentUser('');
    setIsAdmin(false);
    setBootstrapAdminUsername('');
    setItems([]);
    setUsers([]);
    setSettingsOpen(false);
    setViewing(null);
  }

  async function bootstrap() {
    try {
      const session = await getSession();
      setAuthenticated(session.authenticated);
      setCurrentUser(session.username ?? '');
      setIsAdmin(Boolean(session.isAdmin));
      setBootstrapAdminUsername(session.bootstrapAdminUsername ?? '');
      if (session.authenticated) {
        await refreshItems();
        if (session.isAdmin) await refreshUsers();
      }
    } catch {
      resetSession();
    } finally {
      setSessionChecked(true);
    }
  }

  async function refreshItems(silent = false) {
    if (!silent) setRefreshing(true);
    try {
      setItems(await listItems());
    } catch (err) {
      if (err instanceof Error && /authentication required/.test(err.message)) {
        resetSession();
        setError('登录已过期，请重新登录');
      } else if (!silent) {
        showToast(err instanceof Error ? err.message : '刷新失败');
      }
    } finally {
      if (!silent) setRefreshing(false);
    }
  }

  async function refreshUsers() {
    setUsers(await listUsers());
  }

  async function onLogin(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await login(username.trim(), password);
      const session = await getSession();
      setPassword('');
      setAuthenticated(true);
      setCurrentUser(session.username ?? username);
      setIsAdmin(Boolean(session.isAdmin));
      setBootstrapAdminUsername(session.bootstrapAdminUsername ?? '');
      await refreshItems();
      if (session.isAdmin) await refreshUsers();
      else setUsers([]);
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
      resetSession();
    } catch (err) {
      showToast(err instanceof Error ? err.message : '退出失败');
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

  // "粘贴" button: on iPhone there is no Ctrl+V, and long-press → paste into a textarea
  // cannot paste images. The async Clipboard API covers both (iOS shows a "Paste" bubble).
  async function pasteFromClipboard() {
    try {
      if (navigator.clipboard?.read) {
        const clipboardItems = await navigator.clipboard.read();
        const files: File[] = [];
        let pastedText = '';
        for (const clipboardItem of clipboardItems) {
          const imageType = clipboardItem.types.find((type) => type.startsWith('image/'));
          if (imageType) {
            const blob = await clipboardItem.getType(imageType);
            const ext = imageType.split('/')[1] || 'png';
            files.push(new File([blob], `粘贴图片-${Date.now()}.${ext}`, { type: imageType }));
          } else if (clipboardItem.types.includes('text/plain')) {
            pastedText += await (await clipboardItem.getType('text/plain')).text();
          }
        }
        if (files.length) addFiles(files);
        if (pastedText) setMessage((current) => (current ? `${current}\n${pastedText}` : pastedText));
        if (!files.length && !pastedText) showToast('剪贴板为空');
        return;
      }
      const pastedText = await navigator.clipboard.readText();
      if (pastedText) setMessage((current) => (current ? `${current}\n${pastedText}` : pastedText));
      else showToast('剪贴板为空');
    } catch {
      showToast('无法读取剪贴板，请长按输入框粘贴');
    }
  }

  async function submitItem(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!message.trim() && pendingFiles.length === 0) {
      showToast('请输入消息或添加附件');
      return;
    }
    setBusy(true);
    try {
      const created = await createItem(message, pendingFiles.map((entry) => entry.file), itemVisibility);
      setItems((current) => [created, ...current]);
      setMessage('');
      setTab(created.visibility === 'private' ? 'private' : 'shared');
      pendingFiles.forEach((entry) => entry.previewUrl && URL.revokeObjectURL(entry.previewUrl));
      setPendingFiles([]);
      textareaRef.current?.blur();
      showToast('已发送');
    } catch (err) {
      showToast(err instanceof Error ? err.message : '提交失败');
    } finally {
      setBusy(false);
    }
  }

  async function saveUserAccount(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!newUsername.trim() || !newPassword.trim()) {
      showToast('请输入用户名和密码');
      return;
    }
    setBusy(true);
    try {
      await saveUser(newUsername.trim(), newPassword, newIsAdmin);
      setNewUsername('');
      setNewPassword('');
      setNewIsAdmin(false);
      await refreshUsers();
      showToast('用户已保存');
    } catch (err) {
      showToast(err instanceof Error ? err.message : '保存用户失败');
    } finally {
      setBusy(false);
    }
  }

  async function submitPasswordChange(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!currentPassword.trim() || !nextPassword.trim()) {
      showToast('请输入当前密码和新密码');
      return;
    }
    setBusy(true);
    try {
      await changeOwnPassword(currentPassword, nextPassword);
      setCurrentPassword('');
      setNextPassword('');
      resetSession();
      setError('密码修改成功，请使用新密码重新登录');
    } catch (err) {
      showToast(err instanceof Error ? err.message : '修改密码失败');
    } finally {
      setBusy(false);
    }
  }

  async function removeManagedUser(name: string) {
    if (!window.confirm(`删除用户 ${name} 后，该账号及其会话将立即失效，确认删除？`)) return;
    setBusy(true);
    try {
      await deleteUser(name);
      setUsers((current) => current.filter((user) => user.username !== name));
    } catch (err) {
      showToast(err instanceof Error ? err.message : '删除用户失败');
    } finally {
      setBusy(false);
    }
  }

  async function removeItem(id: string) {
    if (!window.confirm('删除后服务器也不会保留备份，确认删除？')) return;
    setBusy(true);
    try {
      await deleteItem(id);
      setItems((current) => current.filter((item) => item.id !== id));
    } catch (err) {
      showToast(err instanceof Error ? err.message : '删除失败');
    } finally {
      setBusy(false);
    }
  }

  async function toggleVisibility(item: Item) {
    const next = item.visibility === 'shared' ? 'private' : 'shared';
    setBusy(true);
    try {
      const updated = await updateItemVisibility(item.id, next);
      setItems((current) => current.map((entry) => (entry.id === updated.id ? { ...entry, visibility: updated.visibility } : entry)));
      showToast(next === 'shared' ? '已改为共享' : '已改为仅自己可见');
    } catch (err) {
      showToast(err instanceof Error ? err.message : '修改可见范围失败');
    } finally {
      setBusy(false);
    }
  }

  async function copyMessage(item: Item) {
    try {
      await navigator.clipboard.writeText(item.message);
      showToast('已复制');
    } catch {
      // Fallback for browsers/contexts without the async Clipboard API.
      const area = document.createElement('textarea');
      area.value = item.message;
      area.setAttribute('readonly', '');
      area.style.position = 'fixed';
      area.style.opacity = '0';
      document.body.appendChild(area);
      area.select();
      area.setSelectionRange(0, area.value.length);
      const ok = document.execCommand('copy');
      area.remove();
      showToast(ok ? '已复制' : '复制失败，请长按文本手动复制');
    }
  }

  async function copyFile(attachment: Attachment) {
    try {
      await copyAttachment(attachment);
      showToast(copyKind(attachment) === 'image' ? '图片已复制，可粘贴到其他 App' : '文件内容已复制');
    } catch (err) {
      showToast(err instanceof Error ? err.message : '复制失败');
    }
  }

  async function shareFile(attachment: Attachment) {
    setFileBusy((cur) => ({ ...cur, [attachment.url]: true }));
    try {
      if ((await shareAttachment(attachment)) === 'retry') showToast('文件已就绪，请再点一次“分享”');
    } catch (err) {
      showToast(err instanceof Error ? err.message : '分享失败');
    } finally {
      setFileBusy((cur) => ({ ...cur, [attachment.url]: false }));
    }
  }

  const sharedItems = useMemo(() => items.filter((item) => item.visibility === 'shared'), [items]);
  const privateItems = useMemo(() => items.filter((item) => item.visibility === 'private'), [items]);
  const visibleItems = tab === 'shared' ? sharedItems : privateItems;
  const closeViewer = useCallback(() => setViewing(null), []);

  if (!sessionChecked) {
    return <div className="center-panel">正在检查登录状态…</div>;
  }

  if (!authenticated) {
    return (
      <div className="login-page">
        <div className="card login-card">
          <h1>共享剪贴板</h1>
          <p className="hint">在多台设备之间共享文字、图片和文件。</p>
          <form onSubmit={onLogin} className="stack gap-16">
            <label>
              <span>用户名</span>
              <input
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
                required
              />
            </label>
            <label>
              <span>密码</span>
              <input value={password} onChange={(e) => setPassword(e.target.value)} type="password" autoComplete="current-password" required />
            </label>
            {error && <div className="error">{error}</div>}
            <button type="submit" className="block" disabled={busy}>{busy ? '登录中…' : '登录'}</button>
          </form>
        </div>
      </div>
    );
  }

  function renderAttachment(attachment: Attachment) {
    const kind = attachmentKind(attachment);
    const copyable = copyKind(attachment);
    const sharing = fileBusy[attachment.url];
    return (
      <div key={attachment.storedName} className="attachment-entry">
        <button type="button" className="attachment-tile" onClick={() => setViewing(attachment)}>
          {attachment.previewUrl ? (
            <img src={attachment.previewUrl} alt={attachment.name} loading="lazy" />
          ) : (
            <div className={`file-icon kind-${kind}`}>{kind === 'video' ? '▶' : fileExtension(attachment.name)}</div>
          )}
          <span className="attachment-name">{attachment.name}</span>
          <span className="attachment-size">{formatSize(attachment.size)}</span>
        </button>
        <div className="attachment-actions">
          {copyable && (
            <button type="button" className="secondary" onClick={() => void copyFile(attachment)}>
              复制
            </button>
          )}
          <button type="button" className="secondary" onClick={() => void shareFile(attachment)} disabled={sharing}>
            {sharing ? '准备中…' : shareLabel}
          </button>
        </div>
      </div>
    );
  }

  function renderItem(item: Item) {
    const long = item.message.length > COLLAPSE_CHARS || item.message.split('\n').length > 8;
    const isExpanded = expanded[item.id];
    const mine = item.createdBy === currentUser;
    return (
      <article key={item.id} className={`item-card ${item.visibility === 'private' ? 'private-item-card' : ''}`}>
        <div className="item-meta">
          <span>{mine ? '我' : item.createdBy || '未知用户'}</span>
          <span>·</span>
          <time dateTime={item.createdAt}>{formatTime(item.createdAt)}</time>
        </div>
        {item.message && (
          <>
            <pre className={`message-block ${long && !isExpanded ? 'collapsed' : ''}`}>{item.message}</pre>
            {long && (
              <button type="button" className="link-button" onClick={() => setExpanded((cur) => ({ ...cur, [item.id]: !isExpanded }))}>
                {isExpanded ? '收起' : '展开全文'}
              </button>
            )}
          </>
        )}
        {item.attachments.length > 0 && <div className="attachment-grid">{item.attachments.map(renderAttachment)}</div>}
        <div className="item-actions">
          {item.message && (
            <button type="button" className="secondary" onClick={() => void copyMessage(item)}>
              复制文本
            </button>
          )}
          {mine && (
            <button
              type="button"
              className="ghost"
              onClick={() => void toggleVisibility(item)}
              disabled={busy}
              title={item.visibility === 'shared' ? '改为仅自己可见' : '共享给所有人'}
            >
              {item.visibility === 'shared' ? '设为仅自己' : '设为共享'}
            </button>
          )}
          <button type="button" className="danger ghost" onClick={() => void removeItem(item.id)} disabled={busy}>
            删除
          </button>
        </div>
      </article>
    );
  }

  return (
    <div className="page">
      <header className="topbar">
        <div className="topbar-title">
          <h1>共享剪贴板</h1>
          <span className="hint">
            <span className={`live-dot ${live ? 'on' : ''}`} title={live ? '实时同步已连接' : '实时同步未连接'} />
            {currentUser}{isAdmin ? ' · 管理员' : ''}
          </span>
        </div>
        <div className="topbar-actions">
          <button type="button" className="secondary icon-button" onClick={() => void refreshItems()} disabled={refreshing} aria-label="刷新">
            <span className={refreshing ? 'spin' : ''}>↻</span>
          </button>
          <button type="button" className="secondary icon-button" onClick={() => setSettingsOpen(true)} aria-label="设置">
            ⚙
          </button>
        </div>
      </header>

      <main className="layout">
        <section className="card composer-card">
          <form onSubmit={submitItem} className="stack gap-12">
            <textarea
              ref={textareaRef}
              value={message}
              onChange={(e) => setMessage(e.target.value)}
              rows={3}
              placeholder="输入或粘贴文字…"
              onPaste={(event) => {
                const files = Array.from(event.clipboardData.files);
                if (files.length > 0) addFiles(files);
              }}
            />
            <div
              className="composer-tools"
              onDragOver={(event) => {
                event.preventDefault();
                event.dataTransfer.dropEffect = 'copy';
              }}
              onDrop={(event) => {
                event.preventDefault();
                addFiles(event.dataTransfer.files);
              }}
            >
              <button type="button" className="secondary" onClick={() => void pasteFromClipboard()}>粘贴</button>
              <button type="button" className="secondary" onClick={() => fileInputRef.current?.click()}>照片 / 文件</button>
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
              <span className="hint drop-hint">也可拖拽文件到这里</span>
            </div>
            {pendingFiles.length > 0 && (
              <div className="pending-strip">
                {pendingFiles.map((entry) => (
                  <div key={entry.id} className="pending-item">
                    {entry.previewUrl ? <img src={entry.previewUrl} alt={entry.file.name} /> : <div className="file-icon">{fileExtension(entry.file.name)}</div>}
                    <span className="attachment-name">{entry.file.name}</span>
                    <span className="attachment-size">{formatSize(entry.file.size)}</span>
                    <button type="button" className="remove-badge" onClick={() => removePendingFile(entry.id)} aria-label={`移除 ${entry.file.name}`}>×</button>
                  </div>
                ))}
              </div>
            )}
            <div className="composer-footer">
              <div className="segmented" role="group" aria-label="可见范围">
                <button type="button" className={itemVisibility === 'shared' ? 'active' : ''} onClick={() => setItemVisibility('shared')}>
                  所有人
                </button>
                <button type="button" className={itemVisibility === 'private' ? 'active' : ''} onClick={() => setItemVisibility('private')}>
                  仅自己
                </button>
              </div>
              <button type="submit" className="send-button" disabled={busy}>{busy ? '发送中…' : '发送'}</button>
            </div>
          </form>
        </section>

        <section className="list-section">
          <div className="segmented tabs" role="tablist">
            <button type="button" role="tab" aria-selected={tab === 'shared'} className={tab === 'shared' ? 'active' : ''} onClick={() => setTab('shared')}>
              所有人可见 <span className="count">{sharedItems.length}</span>
            </button>
            <button type="button" role="tab" aria-selected={tab === 'private'} className={tab === 'private' ? 'active' : ''} onClick={() => setTab('private')}>
              {isAdmin ? '用户独享' : '仅自己'} <span className="count">{privateItems.length}</span>
            </button>
          </div>
          {visibleItems.length === 0 ? (
            <div className="empty">{tab === 'shared' ? '暂无共享内容' : '暂无独享内容'}</div>
          ) : (
            <div className="item-list">{visibleItems.map(renderItem)}</div>
          )}
        </section>
      </main>

      {settingsOpen && (
        <div className="sheet-backdrop" onClick={() => setSettingsOpen(false)}>
          <div className="sheet" role="dialog" aria-modal="true" aria-label="设置" onClick={(e) => e.stopPropagation()}>
            <div className="sheet-header">
              <h2>设置</h2>
              <button type="button" className="secondary" onClick={() => setSettingsOpen(false)}>完成</button>
            </div>

            <section className="sheet-section">
              <h3>修改我的密码</h3>
              <form className="stack gap-12" onSubmit={submitPasswordChange}>
                <input value={currentPassword} onChange={(e) => setCurrentPassword(e.target.value)} type="password" autoComplete="current-password" placeholder="当前密码" />
                <input value={nextPassword} onChange={(e) => setNextPassword(e.target.value)} type="password" autoComplete="new-password" placeholder="新密码" />
                <button type="submit" className="secondary block" disabled={busy}>修改密码</button>
              </form>
            </section>

            <ApiTokensPanel onToast={showToast} />

            {isAdmin && (
              <section className="sheet-section">
                <h3>用户管理</h3>
                <form onSubmit={saveUserAccount} className="stack gap-12">
                  <input
                    value={newUsername}
                    onChange={(e) => setNewUsername(e.target.value)}
                    placeholder="新增 / 重置的用户名"
                    autoCapitalize="none"
                    autoCorrect="off"
                    spellCheck={false}
                  />
                  <input value={newPassword} onChange={(e) => setNewPassword(e.target.value)} type="password" autoComplete="new-password" placeholder="设置密码" />
                  <label className="checkbox-row">
                    <input type="checkbox" checked={newIsAdmin} onChange={(e) => setNewIsAdmin(e.target.checked)} />
                    <span>设为管理员</span>
                  </label>
                  <button type="submit" className="block" disabled={busy}>{busy ? '保存中…' : '保存用户'}</button>
                </form>
                <div className="user-list">
                  {users.map((user) => (
                    <div key={user.username} className="user-row">
                      <div className="managed-user-meta">
                        <strong>{user.username}</strong>
                        <span>{user.isAdmin ? '管理员' : '普通用户'} · {formatTime(user.updatedAt)}</span>
                      </div>
                      <button
                        type="button"
                        className="danger ghost"
                        onClick={() => void removeManagedUser(user.username)}
                        disabled={busy || user.username === currentUser || user.username === bootstrapAdminUsername}
                      >
                        删除
                      </button>
                    </div>
                  ))}
                </div>
              </section>
            )}

            <section className="sheet-section">
              <button type="button" className="danger block" onClick={() => void onLogout()} disabled={busy}>退出登录</button>
              <p className="hint">删除内容即从服务器移除，不保留历史备份。</p>
            </section>
          </div>
        </div>
      )}

      {viewing && <AttachmentViewer attachment={viewing} onClose={closeViewer} onToast={showToast} />}
      {toast && <div className="toast" role="status">{toast}</div>}
    </div>
  );
}

export default App;
