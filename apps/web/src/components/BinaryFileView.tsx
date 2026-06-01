import { Download, FileWarning, Image as ImageIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import type { DraftFile } from "@/lib/api"

/**
 * Maximum bytes for which we attempt an inline image preview. Larger
 * images are shown as a metadata card with a download affordance only.
 */
const MAX_IMAGE_PREVIEW_BYTES = 2 * 1024 * 1024 // 2 MiB

/** Lowercased extensions we treat as previewable bitmap images. */
const BITMAP_EXTS = new Set(["png", "jpg", "jpeg", "gif", "webp"])

function extOf(path: string): string {
  const i = path.lastIndexOf(".")
  return i >= 0 ? path.slice(i + 1).toLowerCase() : ""
}

function humanSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`
}

export interface BinaryFileViewProps {
  /** The draft file to render (binary, oversized, or an image). */
  file: DraftFile
  /** Called when the user asks to download the file. */
  onDownload: () => void
  /**
   * Optional object URL or data URL for an image preview. The parent
   * supplies this only when it holds the raw bytes (e.g. a file the user
   * just uploaded this session); existing binary files have no byte
   * source and render as a metadata card instead.
   */
  imageSrc?: string
}

/**
 * Renders a non-editable draft file: an inline image when we have the
 * bytes, a sanitized SVG, or a metadata card for everything else
 * (oversized text, unknown binary). §13.8 t10.
 *
 * SVG is rendered via an <img> whose src is a data: URL. Loading SVG in
 * an image context disables scripting and external resource loading in
 * every modern browser, so untrusted SVG can't execute — this is the
 * sanitize boundary for SVG, no markup rewriting required. The SVG text
 * lives in the draft (SVG is UTF-8 text), so no extra fetch is needed.
 */
export function BinaryFileView({ file, onDownload, imageSrc }: BinaryFileViewProps) {
  const ext = extOf(file.path)
  const isSvg = ext === "svg"
  const isBitmap = BITMAP_EXTS.has(ext)
  const tooLargeForPreview = file.size > MAX_IMAGE_PREVIEW_BYTES

  // SVG: render the in-draft text as an inert data-URL image.
  const svgSrc =
    isSvg && file.content != null && !tooLargeForPreview
      ? `data:image/svg+xml;utf8,${encodeURIComponent(file.content)}`
      : undefined

  const previewSrc = svgSrc ?? (isBitmap ? imageSrc : undefined)

  return (
    <div className="space-y-3 p-3">
      <MetaCard file={file} ext={ext} onDownload={onDownload} />

      {previewSrc ? (
        <div className="border-border flex items-center justify-center rounded-md border bg-muted/30 p-3">
          <img
            src={previewSrc}
            alt={file.path}
            className="max-h-80 max-w-full object-contain"
          />
        </div>
      ) : (isBitmap || isSvg) && tooLargeForPreview ? (
        <p className="text-muted-foreground text-xs">
          Image is {humanSize(file.size)} (over the {humanSize(MAX_IMAGE_PREVIEW_BYTES)} preview
          limit). Download to view.
        </p>
      ) : isBitmap ? (
        <p className="text-muted-foreground text-xs">
          No inline preview available for this image (download to view, or re-upload to preview).
        </p>
      ) : (
        <p className="text-muted-foreground text-xs">
          This file can&apos;t be edited inline. Download it, or replace it via upload.
        </p>
      )}
    </div>
  )
}

function MetaCard({
  file,
  ext,
  onDownload,
}: {
  file: DraftFile
  ext: string
  onDownload: () => void
}) {
  const isImage = ext === "svg" || BITMAP_EXTS.has(ext)
  return (
    <div className="border-border space-y-2 rounded-md border p-3">
      <div className="flex items-center gap-2">
        {isImage ? (
          <ImageIcon className="text-muted-foreground size-4 shrink-0" aria-hidden />
        ) : (
          <FileWarning className="text-muted-foreground size-4 shrink-0" aria-hidden />
        )}
        <span className="truncate font-mono text-xs" title={file.path}>
          {file.path}
        </span>
      </div>
      <dl className="text-muted-foreground grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-[11px]">
        <dt>Size</dt>
        <dd className="font-mono">{humanSize(file.size)}</dd>
        <dt>Encoding</dt>
        <dd className="font-mono">{file.encoding ?? (file.is_binary ? "binary" : "—")}</dd>
        {file.sha256 ? (
          <>
            <dt>SHA-256</dt>
            <dd className="truncate font-mono" title={file.sha256}>
              {file.sha256.slice(0, 16)}…
            </dd>
          </>
        ) : null}
      </dl>
      <Button size="sm" variant="secondary" onClick={onDownload}>
        <Download className="size-3.5" aria-hidden />
        Download
      </Button>
    </div>
  )
}
