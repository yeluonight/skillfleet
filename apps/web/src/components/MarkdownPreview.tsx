import { useMemo } from "react"
import Markdown from "react-markdown"
import remarkGfm from "remark-gfm"
import rehypeSanitize, { defaultSchema } from "rehype-sanitize"

/**
 * SkillFleet's hardened sanitize schema (§1.3: Markdown preview MUST be
 * sanitized; scripts must never execute).
 *
 * It starts from rehype-sanitize's defaultSchema (already strips
 * <script>, event handlers, and known dangerous constructs) and tightens
 * it further:
 *
 *  - protocols: href/src/cite restricted to http(s)/mailto only — this
 *    is what blocks `javascript:`, `data:`, `vbscript:` and other
 *    URL-borne script. (data: images are intentionally NOT allowed in
 *    the preview; binary/image files have their own viewer in t10.)
 *  - no `style` attribute anywhere (drops a CSS-injection surface).
 *  - links carry rel="nofollow noopener noreferrer" and open in a new
 *    tab, so previewed content can't manipulate the opener or leak the
 *    referrer. These are pinned in the schema (added on every <a>), so
 *    no per-render component override is needed.
 *
 * We deliberately do NOT enable rehype-raw: raw embedded HTML stays
 * escaped rather than parsed, so the only HTML the preview can emit is
 * what react-markdown generates from Markdown nodes, then re-checked by
 * this schema.
 */
const sanitizeSchema: typeof defaultSchema = {
  ...defaultSchema,
  // Restrict URL-bearing attributes to safe protocols. Omitting an entry
  // (e.g. no `data:`) means it's rejected.
  protocols: {
    ...defaultSchema.protocols,
    href: ["http", "https", "mailto"],
    src: ["http", "https"],
    cite: ["http", "https"],
  },
  attributes: {
    ...defaultSchema.attributes,
    // Never allow inline styles on any element.
    "*": (defaultSchema.attributes?.["*"] ?? []).filter((a) => a !== "style"),
    // Pin link hardening: sanitize adds these to every <a> in the output.
    a: [
      ...(defaultSchema.attributes?.a ?? []),
      ["target", "_blank"],
      ["rel", "nofollow noopener noreferrer"],
    ],
  },
}

export interface MarkdownPreviewProps {
  /** Raw Markdown source to render. */
  source: string
  className?: string
}

/**
 * Renders Markdown as sanitized HTML for the draft editor's preview pane.
 *
 * GFM (tables, task lists, strikethrough, autolinks) is enabled via
 * remark-gfm; all output passes through {@link sanitizeSchema}. Images
 * render only from http(s) sources; anything else is dropped by the
 * schema rather than fetched.
 */
export function MarkdownPreview({ source, className }: MarkdownPreviewProps) {
  // The schema object is constant; memo keeps the plugin array stable so
  // react-markdown doesn't reprocess on every parent render. The inner
  // tuple is pinned so it types as a [plugin, options] entry rather than
  // a widened union array (react-markdown wants unified's PluggableList).
  const rehypePlugins = useMemo(
    () => [[rehypeSanitize, sanitizeSchema] as [typeof rehypeSanitize, typeof sanitizeSchema]],
    [],
  )

  return (
    <div
      className={
        "prose prose-sm dark:prose-invert max-w-none break-words text-sm " +
        (className ?? "")
      }
    >
      <Markdown remarkPlugins={[remarkGfm]} rehypePlugins={rehypePlugins}>
        {source}
      </Markdown>
    </div>
  )
}
