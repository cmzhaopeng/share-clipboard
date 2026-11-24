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
      <p className="hint">令牌只能收发剪贴板内容，不能管理账号或密码；只在生成时显示一次，不再使用时请吊销。</p>
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
        <p className="hint">建两个快捷指令：一个发文字/网址，一个发照片/文件。分享时系统只会显示匹配的那个。</p>

        <h5>发文字到剪贴板</h5>
        <ol>
          <li>打开“快捷指令” App，点右上角 <b>＋</b>，把名称改为“发文字到剪贴板”。</li>
          <li>点底部 <b>ⓘ</b>，打开 <b>在共享表单中显示</b>，点“完成”。再点顶部“接收 <b>任何</b> 输入”，只保留 <b>文本</b> 和 <b>URL</b>。</li>
          <li>
            搜索并添加 <b>获取 URL 内容</b>，网址填 <code>{origin}/api/items</code>，点“显示更多”：
            <ul>
              <li>方法：<b>POST</b></li>
              <li>头部：添加新头部，键 <code>Authorization</code>，值 <code>Bearer {shown}</code></li>
              <li>请求体：<b>表单</b>，添加新字段 › <b>文本</b>，键 <code>message</code>，值选变量 <b>输入快捷指令的信息</b>（旧版 iOS 叫“快捷指令输入”）</li>
            </ul>
          </li>
        </ol>

        <h5>发照片/文件到剪贴板（可一次选多张）</h5>
        <p className="hint">“获取 URL 内容”一次只会上传列表里的第一个文件，所以要放进“重复”循环里，每个文件发一次、各成一条。</p>
        <ol>
          <li>新建快捷指令“发文件到剪贴板”，同样打开 <b>在共享表单中显示</b>，接收类型只保留 <b>图像</b>、<b>文件</b>、<b>媒体</b>。</li>
          <li>搜索添加 <b>对每个项目重复</b>。点循环里蓝色的输入，选 <b>输入快捷指令的信息</b>，使它显示为“重复 输入快捷指令的信息 中的每一项”。</li>
          <li>
            搜索添加 <b>获取 URL 内容</b>，长按把它拖到“重复”和“结束重复”之间。设置同上（网址、POST、Authorization 头部），请求体选 <b>表单</b>：
            <ul>
              <li>添加新字段 › <b>文件</b>，键 <code>attachments</code></li>
              <li>点值处的“选取”，在弹出菜单里选变量 <b>重复项目</b>（不要选“文件”去浏览文件 App）</li>
            </ul>
          </li>
          <li>（可选）在“结束重复”下面加 <b>显示通知</b>“已发送”。</li>
        </ol>
        <p className="hint">完成后应是这个结构：</p>
        <pre className="guide-code">{`从 共享表单 接收 图像、文件、媒体
重复 输入快捷指令的信息 中的每一项
    获取 ${origin}/api/items 内容
      方法 POST · 头部 Authorization = Bearer …
      请求体 表单 · attachments = 重复项目
结束重复
显示通知「已发送」`}</pre>
        <p className="hint">
          常见问题：只收到一张 → “获取 URL 内容”不在循环里；收到的都是同一张 → <code>attachments</code> 的值不是“重复项目”；
          上传失败或内容为空 → 点开“重复项目”变量，类型若被改成 App 等或勾选了“名称”等属性，点“清除变量”后重新选“重复项目”，不要再改它的类型和属性。
        </p>

        <h5>可见范围</h5>
        <p className="hint">
          通过令牌发送的内容默认<b>仅自己可见</b>，可在网页上点“设为共享”；若想直接共享，表单里再加文本字段 <code>visibility</code> = <code>shared</code>。
          这些内容会标注“来自 令牌名称”。首次运行时选“始终允许”连接；之后在 Safari、相册、备忘录等 App 中点“分享”，选对应的快捷指令即可。
        </p>

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
