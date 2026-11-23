import { useEffect, useState } from 'react';
import { createToken, listTokens, revokeToken, type ApiToken } from './api';
import { formatTime } from './format';

type Props = {
  onToast: (message: string) => void;
};

async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

export default function ApiTokensPanel({ onToast }: Props) {
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [name, setName] = useState('iPhone 快捷指令');
  const [created, setCreated] = useState<ApiToken | null>(null);
  const [busy, setBusy] = useState(false);
  const origin = window.location.origin;

  useEffect(() => {
    listTokens().then(setTokens).catch(() => onToast('加载令牌失败'));
  }, [onToast]);

  async function create(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!name.trim()) return;
    setBusy(true);
    try {
      const token = await createToken(name.trim());
      setCreated(token);
      setTokens(await listTokens());
    } catch (err) {
      onToast(err instanceof Error ? err.message : '生成失败');
    } finally {
      setBusy(false);
    }
  }

  async function revoke(token: ApiToken) {
    if (!window.confirm(`吊销“${token.name}”后，使用它的快捷指令或脚本将立即失效，确认？`)) return;
    try {
      await revokeToken(token.id);
      setTokens((current) => current.filter((t) => t.id !== token.id));
      if (created?.id === token.id) setCreated(null);
    } catch (err) {
      onToast(err instanceof Error ? err.message : '吊销失败');
    }
  }

  const shown = created?.token ?? '<令牌>';

  return (
    <section className="sheet-section">
      <h3>API 令牌（快捷指令 / 命令行）</h3>
      <p className="hint">令牌等同于你的账号权限，只在生成时显示一次。不再使用时请吊销。</p>
      <form className="token-form" onSubmit={create}>
        <input value={name} onChange={(e) => setName(e.target.value)} maxLength={60} placeholder="令牌名称，如：iPhone 快捷指令" />
        <button type="submit" disabled={busy || !name.trim()}>生成</button>
      </form>

      {created?.token && (
        <div className="token-created">
          <span className="hint">新令牌（请立即复制保存）：</span>
          <code>{created.token}</code>
          <button
            type="button"
            className="secondary block"
            onClick={async () => onToast((await copyText(created.token!)) ? '令牌已复制' : '复制失败，请长按选择')}
          >
            复制令牌
          </button>
        </div>
      )}

      {tokens.length > 0 && (
        <div className="user-list">
          {tokens.map((token) => (
            <div key={token.id} className="user-row">
              <div className="managed-user-meta">
                <strong>{token.name}</strong>
                <span>
                  创建于 {formatTime(token.createdAt)} · {token.lastUsedAt ? `最近使用 ${formatTime(token.lastUsedAt)}` : '尚未使用'}
                </span>
              </div>
              <button type="button" className="danger ghost" onClick={() => void revoke(token)}>吊销</button>
            </div>
          ))}
        </div>
      )}

      <details className="guide">
        <summary>设置 iPhone 快捷指令</summary>
        <h4>① 从任意 App 分享到剪贴板</h4>
        <ol>
          <li>打开“快捷指令” App，点 <b>＋</b> 新建，命名为“发到剪贴板”。</li>
          <li>点底部 <b>ⓘ</b>，打开 <b>在共享表单中显示</b>；接收类型保留“文本、图像、文件”等。</li>
          <li>添加操作 <b>获取类型</b>（输入：快捷指令输入）。</li>
          <li>
            添加 <b>如果</b>：条件选“任意”，<b>类型</b> 是 <b>图像</b>、或 <b>类型</b> 是 <b>文件</b>。
          </li>
          <li>
            在“如果”分支里添加 <b>获取 URL 内容</b>：
            <ul>
              <li>URL：<code>{origin}/api/items</code>，方法：<b>POST</b></li>
              <li>头部：<code>Authorization</code> = <code>Bearer {shown}</code></li>
              <li>请求体：<b>表单</b>，添加 <b>文件</b> 字段，键 <code>attachments</code> = 快捷指令输入</li>
            </ul>
          </li>
          <li>在“否则”分支里添加同样的 <b>获取 URL 内容</b>，表单字段改为 <b>文本</b>，键 <code>message</code> = 快捷指令输入（文字和网址走这里）。</li>
          <li>（可选）在字段里加 <code>visibility</code> = <code>private</code>，即仅自己可见。</li>
        </ol>
        <p className="hint">之后在 Safari、相册、备忘录等 App 中点“分享”，选“发到剪贴板”即可。</p>

        <h4>② 一键取回最新一条</h4>
        <ol>
          <li>新建快捷指令“取剪贴板”，添加 <b>获取 URL 内容</b>：URL <code>{origin}/api/latest</code>，方法 GET，头部同上。</li>
          <li>再添加 <b>拷贝到剪贴板</b>（输入为上一步的结果）。</li>
          <li>可加到主屏幕，或在“设置 › 辅助功能 › 触控 › 轻点背面”中绑定。</li>
        </ol>
        <p className="hint">最新一条有文字时返回文字；只有附件时返回第一个文件本身（图片可直接粘贴）。</p>

        <h4>③ 电脑命令行</h4>
        <pre className="guide-code">{`TOKEN=${shown}
# 发送文字
curl -H "Authorization: Bearer $TOKEN" -F message="hello" ${origin}/api/items
# 发送文件
curl -H "Authorization: Bearer $TOKEN" -F attachments=@report.pdf ${origin}/api/items
# 取最新一条
curl -H "Authorization: Bearer $TOKEN" ${origin}/api/latest`}</pre>
      </details>
    </section>
  );
}
