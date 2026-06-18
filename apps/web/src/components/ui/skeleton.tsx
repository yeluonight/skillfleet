import { cn } from "@/lib/utils"

// Skeleton — a pulsing placeholder block for loading states. Sized by the
// caller via className (h-/w-/rounded-).
function Skeleton({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="skeleton"
      className={cn("bg-accent animate-pulse rounded-md", className)}
      {...props}
    />
  )
}

export { Skeleton }
