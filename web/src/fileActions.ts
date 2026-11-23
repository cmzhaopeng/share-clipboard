import type { Attachment } from './api';

export type Kind = 'image' | 'video' | 'audio' | 'pdf' | 'text' | 'other';

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

export const canShareFiles = (() => {
  try {
    return (
      typeof navigator.share === 'function' &&
      typeof navigator.canShare === 'function' &&
      navigator.canShare({ files: [new File(['x'], 'x.txt', { type: 'text/plain' })] })
    );
  } catch {
    return false;
  }
})();

const TEXT_COPY_LIMIT = 2 * 1024 * 1024;

/** What "复制" puts on the clipboard. Browsers only accept text and PNG images, never arbitrary files. */
export function copyKind(attachment: Attachment): 'image' | 'text' | null {
  const kind = attachmentKind(attachment);
  if (kind === 'image' && attachment.previewUrl) return 'image';
  if (kind === 'text' && attachment.size <= TEXT_COPY_LIMIT) return 'text';
  return null;
}

// Small in-memory cache so the viewer and the list buttons don't download the same file twice,
// and so a second tap on "分享" can call navigator.share() immediately.
const CACHE_LIMIT = 60 * 1024 * 1024;
const cache = new Map<string, File>();
let cacheBytes = 0;

export function cachedFile(attachment: Attachment): File | undefined {
  return cache.get(attachment.url);
}

export async function loadAttachmentFile(attachment: Attachment, signal?: AbortSignal): Promise<File> {
  const hit = cache.get(attachment.url);
  if (hit) return hit;
  const response = await fetch(attachment.url, { credentials: 'include', signal });
  if (response.status === 401) throw new Error('登录已失效，请重新登录');
  if (!response.ok) throw new Error(`加载失败：${response.status}`);
  const blob = await response.blob();
  const file = new File([blob], attachment.name, { type: attachment.mimeType || 'application/octet-stream' });
  if (file.size <= CACHE_LIMIT) {
    cache.set(attachment.url, file);
    cacheBytes += file.size;
    for (const [key, value] of cache) {
      if (cacheBytes <= CACHE_LIMIT) break;
      cache.delete(key);
      cacheBytes -= value.size;
    }
  }
  return file;
}

async function toPngBlob(file: Blob): Promise<Blob> {
  if (file.type === 'image/png') return new Blob([file], { type: 'image/png' });
  const bitmap = await createImageBitmap(file);
  const canvas = document.createElement('canvas');
  canvas.width = bitmap.width;
  canvas.height = bitmap.height;
  canvas.getContext('2d')?.drawImage(bitmap, 0, 0);
  bitmap.close();
  return new Promise((resolve, reject) => canvas.toBlob((blob) => (blob ? resolve(blob) : reject(new Error('图片转换失败'))), 'image/png'));
}

export async function copyAttachment(attachment: Attachment): Promise<void> {
  const kind = copyKind(attachment);
  if (!kind) throw new Error('此类型不支持复制，请使用分享');
  if (typeof ClipboardItem !== 'undefined' && navigator.clipboard?.write) {
    // ClipboardItem is created synchronously inside the tap with a *promise* of the data:
    // Safari only allows clipboard writes during the user gesture, but lets the data arrive later.
    const type = kind === 'image' ? 'image/png' : 'text/plain';
    const data = loadAttachmentFile(attachment).then((file) => (kind === 'image' ? toPngBlob(file) : new Blob([file], { type: 'text/plain' })));
    await navigator.clipboard.write([new ClipboardItem({ [type]: data })]);
    return;
  }
  if (kind === 'text' && navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(await (await loadAttachmentFile(attachment)).text());
    return;
  }
  throw new Error('当前浏览器不支持复制图片，请使用分享');
}

export function saveFileViaLink(file: File) {
  const url = URL.createObjectURL(file);
  const link = document.createElement('a');
  link.href = url;
  link.download = file.name;
  document.body.appendChild(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
}

/**
 * Opens the system share sheet (iOS: 拷贝 / 存储到“文件” / 微信 / 邮件 …), or downloads where file sharing
 * is unsupported. Returns 'retry' when the download took too long and the browser no longer treats the
 * call as part of the tap: the file is cached by then, so the next tap shares immediately.
 */
export async function shareAttachment(attachment: Attachment): Promise<'done' | 'retry'> {
  const wasCached = Boolean(cache.get(attachment.url));
  const file = await loadAttachmentFile(attachment);
  if (canShareFiles && navigator.canShare({ files: [file] })) {
    try {
      await navigator.share({ files: [file] });
    } catch (err) {
      const name = (err as DOMException)?.name;
      if (name === 'AbortError') return 'done';
      if (name === 'NotAllowedError' && !wasCached) return 'retry';
      throw err;
    }
    return 'done';
  }
  saveFileViaLink(file);
  return 'done';
}

export const shareLabel = canShareFiles ? '分享' : '下载';
