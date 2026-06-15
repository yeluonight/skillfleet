import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Cpu, Copy, RefreshCw, AlertCircle } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"

import { api, apiErrorMessage } from "@/lib/api"
import type { CreateEnrollmentTokenResponse, EnrollmentToken } from "@/lib/api"
import { useApiResource } from "@/hooks/useApiResource"
import { useAsyncAction } from "@/hooks/useAsyncAction"

// DevicesCard surfaces the "generate enrollment token" action and a
// compact list of recent tokens. The full Devices list (with approve
// / revoke actions) lives in DevicesList.tsx and renders alongside
// this card in the dashboard.
export function DevicesCard() {
  const { t } = useTranslation()
  const [issued, setIssued] = useState<CreateEnrollmentTokenResponse | null>(null)
  // Captured once at mount + on every list refresh so the render path
  // never calls Date.now() (eslint react-hooks/purity).
  const [renderedAt, setRenderedAt] = useState(() => Date.now())
  const {
    data: tokensData,
    error: loadError,
    refresh,
    setError: setLoadError,
  } = useApiResource<{ tokens: EnrollmentToken[] }>(
    async () => {
      const res = await api.listEnrollmentTokens()
      setRenderedAt(Date.now())
      return res
    },
    { errorFallback: "Failed to load tokens." },
  )
  const tokens = tokensData?.tokens ?? null
  const action = useAsyncAction()
  const error = action.error ?? loadError

  async function generate() {
    setLoadError(null)
    const ok = await action.run(async () => {
      const res = await api.createEnrollmentToken()
      setIssued(res)
    }, "Failed to create token.")
    if (ok) void refresh()
  }

  async function revoke(id: string) {
    try {
      await api.revokeEnrollmentToken(id)
      void refresh()
    } catch (err) {
      setLoadError(apiErrorMessage(err, "Failed to revoke token."))
    }
  }

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle className="text-lg flex items-center gap-2">
            <Cpu className="text-primary size-5" aria-hidden />
            {t("devices.title")}
          </CardTitle>
          <CardDescription>
            {t("devices.enrollDescPrefix")}
            <code className="font-mono text-xs">
              skillfleet-agent enroll &lt;url&gt; &lt;token&gt;
            </code>
            {t("devices.enrollDescSuffix")}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex items-center gap-2">
            <Button onClick={generate} disabled={action.busy}>
              {action.busy ? t("devices.generating") : t("devices.generateToken")}
            </Button>
            <Button variant="ghost" size="sm" onClick={refresh} disabled={action.busy}>
              <RefreshCw className="size-4" aria-hidden />
              {t("common.refresh")}
            </Button>
          </div>

          {error && (
            <Alert variant="destructive">
              <AlertCircle className="size-4" aria-hidden />
              <AlertTitle>{t("devices.tokenError")}</AlertTitle>
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}

          <TokenList tokens={tokens} renderedAt={renderedAt} onRevoke={revoke} />
        </CardContent>
      </Card>

      <IssuedTokenDialog issued={issued} onClose={() => setIssued(null)} />
    </>
  )
}

function TokenList({
  tokens,
  renderedAt,
  onRevoke,
}: {
  tokens: EnrollmentToken[] | null
  renderedAt: number
  onRevoke: (id: string) => void
}) {
  const { t } = useTranslation()
  if (tokens === null) {
    return <p className="text-muted-foreground text-sm">{t("devices.loadingTokens")}</p>
  }
  if (tokens.length === 0) {
    return <p className="text-muted-foreground text-sm">{t("devices.noTokens")}</p>
  }
  return (
    <ul className="divide-border divide-y rounded-md border">
      {tokens.map((tok) => {
        const expired = tok.status === "pending" && tok.expires_at < renderedAt
        return (
          <li key={tok.id} className="flex items-center justify-between gap-4 px-3 py-2">
            <div className="space-y-0.5">
              <div className="font-mono text-xs">{tok.id}</div>
              <div className="text-muted-foreground text-xs">
                <TokenStatusBadge status={expired ? "expired" : tok.status} />
                <span className="ml-2">
                  {t("devices.expiresAt", { time: new Date(tok.expires_at).toLocaleString() })}
                </span>
              </div>
            </div>
            {tok.status === "pending" && !expired && (
              <Button variant="ghost" size="sm" onClick={() => onRevoke(tok.id)}>
                {t("devices.revoke")}
              </Button>
            )}
          </li>
        )
      })}
    </ul>
  )
}

// TokenStatusBadge colours the enrollment-token lifecycle (pending/used/
// revoked/expired) with semantic state tokens; the label is localised.
function TokenStatusBadge({ status }: { status: string }) {
  const { t } = useTranslation()
  const colour =
    status === "pending"
      ? "text-state-warn-600"
      : status === "used"
        ? "text-state-clean-600"
        : "text-muted-foreground"
  const label =
    status === "pending"
      ? t("devices.tokenStatus.pending")
      : status === "used"
        ? t("devices.tokenStatus.used")
        : status === "revoked"
          ? t("devices.tokenStatus.revoked")
          : t("devices.tokenStatus.expired")
  return <span className={`font-medium uppercase tracking-wide ${colour}`}>{label}</span>
}

function IssuedTokenDialog({
  issued,
  onClose,
}: {
  issued: CreateEnrollmentTokenResponse | null
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)

  async function copyToken() {
    if (!issued) return
    try {
      await navigator.clipboard.writeText(issued.token)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      // Clipboard API can be blocked (e.g. insecure context). The
      // operator can still select-and-copy the visible value.
    }
  }

  return (
    <Dialog
      open={issued !== null}
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("devices.newTokenTitle")}</DialogTitle>
          <DialogDescription>
            {t("devices.newTokenDesc", {
              time: issued ? new Date(issued.expires_at).toLocaleString() : "",
            })}
          </DialogDescription>
        </DialogHeader>
        {issued && (
          <div className="space-y-3">
            <div className="bg-muted text-foreground break-all rounded-md p-3 font-mono text-sm">
              {issued.token}
            </div>
            <Button onClick={copyToken} className="w-full" variant="secondary">
              <Copy className="size-4" aria-hidden />
              {copied ? t("devices.copied") : t("devices.copyToken")}
            </Button>
            <p className="text-muted-foreground text-xs">
              {t("devices.runOnAgentHost")}
              <br />
              <code className="font-mono">
                skillfleet-agent enroll &lt;server-url&gt; {issued.token}
              </code>
            </p>
          </div>
        )}
        <DialogFooter>
          <Button onClick={onClose} variant="ghost" className="w-full sm:w-auto">
            {t("devices.done")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
