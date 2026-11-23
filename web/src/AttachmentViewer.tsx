import { useEffect, useState } from 'react';
import type { Attachment } from './api';
import { formatSize } from './format';

type Kind = 'image' | 'video' | 'audio' | 'pdf' | 'text' | 'other';

// Files up to this size are fetched as soon as the viewer opens, so the share button can call
// navigator.share() directly inside the tap (iOS rejects share() once the tap is "used up").
const AUTO_FETCH_LIMIT = 15 * 1024 * 1024;

export function attachmentKind(attachment: Attachment): Kind {
  const type = attachment.mimeType.split(';')[0].trim().toLowerCase();
  if (attachment.previewUrl) return 'image';
  if (!attachment.inlineUrl) return 'other';
  if (type.startsWith('image/')) return 'image';
  if (type.startsWith('video/')) return 'video';
  if (type.startsWith('audio/')) return 'audio';
  if (type === 'application/pdf') return 'pdf';
  return 'text';
}

export function isStandalone(): boolean {
  return window.matchMedia('(display-mode: standalone)').matches || Boolean((navigator as Navigator & { standalone?: boolean }).standalone);
}

export const isIOS =
  /iP(hone|ad|od)/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);

function saveFileViaLink(file: File) {
  const url = URL.createObjectURL(file);
  const link = document.createElement('a');
  link.href = url;
  link.download = file.name;
  document.body.appendChild(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
}

type Props = {
  attachment: Attachment;
  onClose: () => void;
  onToast: (message: string) => void;
};

export default function AttachmentViewer({ attachment, onClose, onToast }: Props) {
  const kind = attachmentKind(attachment);
  const [file, setFile] = useState<File | null>(null);
  const [text, setText] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const canShareFiles = typeof navigator.share === 'function' && typeof navigator.canShare === 'function';

  async function fetchFile(signal?: AbortSignal): Promise<File | null> {
    setLoading(true);
    setLoadError(null);
    try {
      const response = await fetch(attachment.url, { credentials: 'include', signal });
      if (response.status === 401) throw new Error('登录已失效，请重新登录');
      if (!response.ok) throw new Error(`加载失败：${response.status}`);
      const blob = await response.blob();
      const loaded = new File([blob], attachment.name, { type: attachment.mimeType || 'application/octet-stream' });
      setFile(loaded);
      if (kind === 'text') setText(await loaded.text());
      return loaded;
    } catch (err) {
      if (!signal?.aborted) setLoadError(err instanceof Error ? err.message : '加载失败');
      return null;
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }

  useEffect(() => {
    const controller = new AbortController();
    if (attachment.size <= AUTO_FETCH_LIMIT) void fetchFile(controller.signal);
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [attachment.url]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => event.key === 'Escape' && onClose();
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    window.addEventListener('keydown', onKey);
    return () => {
      document.body.style.overflow = previousOverflow;
      window.removeEventListener('keydown', onKey);
    };
  }, [onClose]);

  async function shareOrSave() {
    if (!file) {
      // Large file: this tap only downloads it; the next tap shares it.
      if (await fetchFile()) onToast('文件已就绪，请再点一次“存储 / 分享”');
      return;
    }
    const data = { files: [file] };
    if (canShareFiles && navigator.canShare(data)) {
      try {
        await navigator.share(data);
      } catch (err) {
        if ((err as DOMException)?.name !== 'AbortError') onToast('无法调用分享，已改为下载');
        else return;
        saveFileViaLink(file);
      }
      return;
    }
    saveFileViaLink(file);
  }

  const shareLabel = !file && attachment.size > AUTO_FETCH_LIMIT ? `准备文件（${formatSize(attachment.size)}）` : isIOS ? '存储 / 分享' : '下载';

  return (
    <div className="viewer" role="dialog" aria-modal="true" aria-label={attachment.name}>
      <div className="viewer-bar viewer-top">
        <button type="button" className="secondary" onClick={onClose}>关闭</button>
        <div className="viewer-title">
          <strong>{attachment.name}</strong>
          <span>{formatSize(attachment.size)}</span>
        </div>
      </div>

      <div className={`viewer-body viewer-${kind}`}>
        {kind === 'image' && <img src={attachment.previewUrl || attachment.inlineUrl} alt={attachment.name} />}
        {kind === 'video' && <video src={attachment.inlineUrl} controls playsInline preload="metadata" />}
        {kind === 'audio' && <audio src={attachment.inlineUrl} controls preload="metadata" />}
        {kind === 'pdf' && <iframe src={attachment.inlineUrl} title={attachment.name} />}
        {kind === 'text' && (
          <pre className="viewer-text">{text ?? (loadError || (attachment.size > AUTO_FETCH_LIMIT ? '文件较大，请点击下方按钮获取。' : '加载中…'))}</pre>
        )}
        {kind === 'other' && (
          <div className="viewer-placeholder">
            <div className="file-icon">{fileExtension(attachment.name)}</div>
            <p>此类型无法在页面内预览。</p>
            <p className="hint">{isIOS ? '点击下方“存储 / 分享”，可存到“文件”或用其他 App 打开。' : '点击下方按钮下载。'}</p>
          </div>
        )}
      </div>

      <div className="viewer-bar viewer-bottom">
        {loadError && kind !== 'text' && <span className="viewer-error">{loadError}</span>}
        {kind === 'pdf' && isIOS && <span className="hint">若只显示首页，可用“存储 / 分享”在“文件”中打开</span>}
        <div className="viewer-actions">
          {attachment.inlineUrl && !isStandalone() && (
            <a className="button secondary" href={attachment.inlineUrl} target="_blank" rel="noreferrer">新窗口打开</a>
          )}
          <button type="button" onClick={() => void shareOrSave()} disabled={loading}>
            {loading ? '加载中…' : shareLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

export function fileExtension(name: string): string {
  const dot = name.lastIndexOf('.');
  const ext = dot > 0 ? name.slice(dot + 1) : '';
  return (ext || 'FILE').slice(0, 5).toUpperCase();
}
