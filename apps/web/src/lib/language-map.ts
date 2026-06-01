// Language mapping for Monaco Editor based on file extension.
// Implements the v1.0 §7.4 15-language mapping.

/**
 * Returns the Monaco language identifier for the given file path.
 * Extension matching is case-insensitive. Unknown or missing extensions
 * default to "plaintext".
 *
 * Implements v1.0 §7.4: 15 extension-to-language mappings.
 */
export function languageForPath(path: string): string {
  const lastDot = path.lastIndexOf(".")
  const ext = lastDot !== -1 ? path.slice(lastDot).toLowerCase() : ""

  switch (ext) {
    case ".md":
      return "markdown"
    case ".yaml":
    case ".yml":
      return "yaml"
    case ".json":
      return "json"
    // Monaco does not ship a native TOML language; "ini" provides
    // reasonable approximate syntax highlighting for tables and keys.
    case ".toml":
      return "ini"
    case ".py":
      return "python"
    case ".sh":
      return "shell"
    case ".ps1":
      return "powershell"
    case ".bat":
    case ".cmd":
      return "bat"
    case ".js":
      return "javascript"
    case ".ts":
      return "typescript"
    case ".css":
      return "css"
    case ".html":
      return "html"
    case ".txt":
      return "plaintext"
    default:
      return "plaintext"
  }
}