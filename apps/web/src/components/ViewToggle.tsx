import { Button } from "@/components/ui/button"

// ViewToggle is the two-option "by tool / by path" segmented control shared by
// the fleet matrix and the device inventory matrix. Labels are passed in so
// each caller keeps its own i18n namespace; the selected option renders as a
// solid button, the other as an outline.
export function ViewToggle<T extends string>({
  value,
  options,
  onChange,
  label,
}: {
  value: T
  options: { value: T; label: string }[]
  onChange: (value: T) => void
  label?: string
}) {
  return (
    <div className="flex items-center justify-end gap-1">
      {label ? <span className="text-muted-foreground mr-1 text-xs">{label}</span> : null}
      {options.map((o) => (
        <Button
          key={o.value}
          variant={value === o.value ? "default" : "outline"}
          size="xs"
          onClick={() => onChange(o.value)}
        >
          {o.label}
        </Button>
      ))}
    </div>
  )
}
