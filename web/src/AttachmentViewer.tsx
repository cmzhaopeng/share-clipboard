import { useEffect, useState } from 'react';
import type { Attachment } from './api';
import { formatSize } from './format';
import {
  attachmentKind,
  cachedFile,
  canShareFiles,
  copyAttachment,
  copyKind,
  isIOS,
  isStandalone,
  loadAttachmentFile,
  shareAttachment,
  shareLabel,
} from './fileActions';

// Files up to this size are fetched as soon as the viewer opens, so "分享" can open the share
// sheet within the tap (iOS rejects share() once the tap is "used up").
const AUTO_FETCH_LIMIT = 15 * 1024 * 1024;

type Props = {
  attachment: Attachment;
  onClose: () => void;
  onToast: (message: string) => void;
};

export default function AttachmentViewer({ attachment, onClose, onToast }: Props) {
  const kind = attachmentKind(attachment);
  const copyable = copyKind(attachment);
  const [ready, setReady] = useState(Boolean(cachedFile(attachment)));
  const [text, setText] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);

  useEffect(() => {
    if (attachment.size > AUTO_FETCH_LIMIT) return;
    const controller = new AbortController();
    setLoading(true);
    loadAttachmentFile(attachment, controller.signal)
      .then(async (file) => {
        setReady(true);
        if (kind === 'text') setText(await file.text());
      })
      .catch((err) => !controller.signal.aborted && setLoadError(err instanceof Error ? err.message : '加载失败'))
      .finally(() => !controller.signal.aborted && setLoading(false));
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

  async function share() {
    setLoading(true);
    try {
      const result = await shareAttachment(attachment);
      setReady(true);
      if (result === 'retry') onToast('文件已就绪，请再点一次“分享”');
    } catch (err) {
      onToast(err instanceof Error ? err.message : '分享失败');
    } finally {
      setLoading(false);
    }
  }

  async function copy() {
    try {
      await copyAttachment(attachment);
      onToast(copyable === 'image' ? '图片已复制' : '内容已复制');
    } catch (err) {
      onToast(err instanceof Error ? err.message : '复制失败');
    }
  }

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
          <pre className="viewer-text">{text ?? (loadError || (attachment.size > AUTO_FETCH_LIMIT ? '文件较大，请点击下方“分享”获取。' : '加载中…'))}</pre>
        )}
        {kind === 'other' && (
          <div className="viewer-placeholder">
            <div className="file-icon">{fileExtension(attachment.name)}</div>
            <p>此类型无法在页面内预览。</p>
            <p className="hint">{canShareFiles ? '点击下方“分享”，可拷贝、存到“文件”或发送到其他 App。' : '点击下方按钮下载。'}</p>
          </div>
        )}
      </div>

      <div className="viewer-bar viewer-bottom">
        {loadError && kind !== 'text' && <span className="viewer-error">{loadError}</span>}
        {kind === 'pdf' && isIOS && <span className="hint">若只显示首页，可通过“分享”在“文件”中打开</span>}
        <div className="viewer-actions">
          {attachment.inlineUrl && !isStandalone() && (
            <a className="button secondary" href={attachment.inlineUrl} target="_blank" rel="noreferrer">新窗口打开</a>
          )}
          {copyable && (
            <button type="button" className="secondary" onClick={() => void copy()}>
              复制
            </button>
          )}
          <button type="button" onClick={() => void share()} disabled={loading && !ready}>
            {loading && !ready ? '加载中…' : shareLabel}
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
