import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandShortcut,
} from "@/components/ui/command"
import { useTranslation } from "react-i18next"

/**
 * A single action that can be invoked from the command palette.
 */
export interface CommandAction {
  id: string
  /** Primary label used for display and search. */
  label: string
  /** Extra search keywords (e.g. "保存 save") appended to the search value. */
  keywords?: string
  /** Display-only shortcut hint shown on the right side, e.g. "Ctrl+S". */
  shortcut?: string
  run: () => void
  disabled?: boolean
}

export interface CommandPaletteProps {
  /** Whether the palette dialog is visible. */
  open: boolean
  /** Called when the dialog requests to open or close. */
  onOpenChange: (open: boolean) => void
  /** The list of available commands. */
  actions: CommandAction[]
}

/**
 * A Cmd/Ctrl+K-style command palette that renders actions in a searchable
 * CommandDialog. The parent is responsible for toggling `open` (usually via a
 * global keydown listener); this component only manages the dialog itself and
 * delegates close/select back via `onOpenChange`.
 */
export function CommandPalette({ open, onOpenChange, actions }: CommandPaletteProps) {
  const { t } = useTranslation()
  return (
    <CommandDialog open={open} onOpenChange={onOpenChange}>
      <CommandInput placeholder={t("skills.palette.placeholder")} />
      <CommandList>
        <CommandEmpty>{t("skills.palette.empty")}</CommandEmpty>
        <CommandGroup heading={t("skills.palette.heading")}>
          {actions.map((action) => (
            <CommandItem
              key={action.id}
              value={`${action.label} ${action.keywords ?? ""}`}
              disabled={action.disabled}
              onSelect={() => {
                action.run()
                onOpenChange(false)
              }}
            >
              <span>{action.label}</span>
              {action.shortcut && (
                <CommandShortcut>{action.shortcut}</CommandShortcut>
              )}
            </CommandItem>
          ))}
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  )
}