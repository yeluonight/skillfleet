import { useCallback, useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { Rocket, Copy, Check, Loader2, Terminal, MonitorSmartphone, AlertCircle } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { api, apiErrorMessage } from "@/lib/api"
import type { CreateEnrollmentTokenResponse, Device } from "@/lib/api"

// EnrollWizard is the guided device-enrollment flow (Phase 12 t13), the fix
// for the "I had to go to the command line to approve" confusion: it makes the
// CLI-vs-WebUI split explicit. Four steps — (1) mint a token here, (2) run the
// enroll command on the agent host, (3) watch the device appear and approve it
// here, (4) register a skill root on the agent host — each tagged with whether
// it happens in the WebUI or on the agent's command line. It reuses the same
// API the cards use; it only adds the step orchestration + the assembled
// commands with copy buttons.
export function EnrollWizard({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation()
  const [step, setStep] = useState(1)
  const [issued, setIssued] = useState<CreateEnrollmentTokenResponse | null>(null)

  // The server URL the agent must connect to is exactly the origin the
  // operator is viewing the WebUI on (hash router → strip the #/… part).
  const serverUrl = window.location.origin

  function reset() {
    setStep(1)
    setIssued(null)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) reset()
        onOpenChange(o)
      }}
    >
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Rocket className="text-primary size-5" aria-hidden />
            {t("enroll.wizardTitle")}
          </DialogTitle>
          <DialogDescription>{t("enroll.wizardDesc")}</DialogDescription>
        </DialogHeader>

        <div className="text-muted-foreground text-xs">{t("enroll.step", { n: step })} / 4</div>

        {step === 1 && <StepGenerate issued={issued} setIssued={setIssued} />}
        {step === 2 && <StepEnrollCommand serverUrl={serverUrl} token={issued?.token ?? ""} />}
        {step === 3 && <StepApprove />}
        {step === 4 && <StepRoots />}

        <div className="flex items-center justify-between gap-2 pt-2">
          <Button variant="ghost" size="sm" onClick={() => setStep((s) => Math.max(1, s - 1))} disabled={step === 1}>
            {t("enroll.back")}
          </Button>
          {step < 4 ? (
            <Button size="sm" onClick={() => setStep((s) => s + 1)} disabled={step === 1 && !issued}>
              {t("enroll.next")}
            </Button>
          ) : (
            <Button
              size="sm"
              onClick={() => {
                reset()
                onOpenChange(false)
              }}
            >
              {t("enroll.finish")}
            </Button>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

// StepBadge marks whether a step happens in the WebUI or on the agent's CLI.
function StepBadge({ cli }: { cli: boolean }) {
  const { t } = useTranslation()
  return cli ? (
    <Badge className="bg-state-warn-50 text-state-warn-600 border-state-warn-500/30 gap-1">
      <Terminal className="size-3" aria-hidden />
      {t("enroll.cliBadge")}
    </Badge>
  ) : (
    <Badge className="bg-state-info-50 text-state-info-600 border-state-info-500/30 gap-1">
      <MonitorSmartphone className="size-3" aria-hidden />
      {t("enroll.webBadge")}
    </Badge>
  )
}

// CommandBlock shows a shell command with a copy button. The command body
// stays in English (it's literal CLI); only the surrounding chrome localises.
function CommandBlock({ command }: { command: string }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  async function copy() {
    try {
      await navigator.clipboard.writeText(command)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      // Clipboard blocked (insecure context) — operator can select manually.
    }
  }
  return (
    <div className="space-y-2">
      <div className="bg-muted text-foreground break-all rounded-md p-3 font-mono text-xs">{command}</div>
      <Button variant="secondary" size="sm" onClick={copy}>
        {copied ? <Check className="size-4" aria-hidden /> : <Copy className="size-4" aria-hidden />}
        {copied ? t("common.copied") : t("enroll.copyCommand")}
      </Button>
    </div>
  )
}

function StepGenerate({
  issued,
  setIssued,
}: {
  issued: CreateEnrollmentTokenResponse | null
  setIssued: (v: CreateEnrollmentTokenResponse) => void
}) {
  const { t } = useTranslation()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function generate() {
    setBusy(true)
    setError(null)
    try {
      setIssued(await api.createEnrollmentToken())
    } catch (err) {
      setError(apiErrorMessage(err, t("enroll.generateFailed")))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <h3 className="text-sm font-semibold">{t("enroll.step1Title")}</h3>
        <StepBadge cli={false} />
      </div>
      <p className="text-muted-foreground text-sm">{t("enroll.step1Desc")}</p>
      <Button onClick={generate} disabled={busy}>
        {busy ? t("enroll.generating") : t("enroll.generate")}
      </Button>
      {error && (
        <Alert variant="destructive">
          <AlertCircle className="size-4" aria-hidden />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {issued && (
        <p className="text-state-clean-600 text-sm">
          {t("enroll.tokenReady", { time: new Date(issued.expires_at).toLocaleString() })}
        </p>
      )}
    </div>
  )
}

function StepEnrollCommand({ serverUrl, token }: { serverUrl: string; token: string }) {
  const { t } = useTranslation()
  const command = `skillfleet-agent enroll ${serverUrl} ${token}`
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <h3 className="text-sm font-semibold">{t("enroll.step2Title")}</h3>
        <StepBadge cli={true} />
      </div>
      <p className="text-muted-foreground text-sm">{t("enroll.step2Desc")}</p>
      <CommandBlock command={command} />
      <p className="text-muted-foreground text-xs">{t("enroll.step2Hint")}</p>
    </div>
  )
}

function StepApprove() {
  const { t } = useTranslation()
  const [devices, setDevices] = useState<Device[] | null>(null)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const tokenRef = useRef(0)

  const refresh = useCallback(async () => {
    const my = ++tokenRef.current
    try {
      const res = await api.listDevices()
      if (my === tokenRef.current) setDevices(res.devices)
    } catch {
      // Silent: the wizard polls; a transient failure just retries next tick.
    }
  }, [])

  // Poll the device list every 3s while this step is mounted so a freshly
  // enrolled agent shows up without a manual refresh. The first fetch is
  // deferred off the effect body (setTimeout 0) so the synchronous effect
  // never triggers a setState directly.
  useEffect(() => {
    const first = window.setTimeout(() => void refresh(), 0)
    const id = window.setInterval(() => void refresh(), 3000)
    return () => {
      window.clearTimeout(first)
      window.clearInterval(id)
    }
  }, [refresh])

  async function approve(id: string) {
    setBusyId(id)
    setError(null)
    try {
      await api.approveDevice(id)
      await refresh()
    } catch (err) {
      setError(apiErrorMessage(err, t("enroll.approveFailed")))
    } finally {
      setBusyId(null)
    }
  }

  const pending = devices?.filter((d) => d.status === "pending") ?? []

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <h3 className="text-sm font-semibold">{t("enroll.step3Title")}</h3>
        <StepBadge cli={false} />
      </div>
      <p className="text-muted-foreground text-sm">{t("enroll.step3Desc")}</p>
      {error && (
        <Alert variant="destructive">
          <AlertCircle className="size-4" aria-hidden />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div className="text-muted-foreground flex items-center gap-2 text-xs">
        <Loader2 className="size-3 animate-spin" aria-hidden />
        {t("enroll.waiting")}
      </div>
      {pending.length === 0 ? (
        <p className="text-muted-foreground text-sm">{t("enroll.noPendingYet")}</p>
      ) : (
        <ul className="divide-border divide-y rounded-md border">
          {pending.map((d) => (
            <li key={d.id} className="flex items-center justify-between gap-3 px-3 py-2">
              <div className="min-w-0">
                <div className="truncate text-sm font-medium">{d.name}</div>
                <div className="text-muted-foreground font-mono text-[10px]">{d.id}</div>
              </div>
              <Button size="sm" onClick={() => approve(d.id)} disabled={busyId === d.id}>
                {busyId === d.id ? t("enroll.approving") : t("enroll.approve")}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function StepRoots() {
  const { t } = useTranslation()
  const command = `skillfleet-agent roots add <dir>`
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <h3 className="text-sm font-semibold">{t("enroll.step4Title")}</h3>
        <StepBadge cli={true} />
      </div>
      <p className="text-muted-foreground text-sm">{t("enroll.step4Desc")}</p>
      <p className="text-muted-foreground text-xs">{t("enroll.step4Cmd")}</p>
      <CommandBlock command={command} />
      <p className="text-muted-foreground text-xs">{t("enroll.step4Hint")}</p>
      <Alert>
        <Check className="size-4" aria-hidden />
        <AlertTitle>{t("enroll.doneTitle")}</AlertTitle>
        <AlertDescription>{t("enroll.doneDesc")}</AlertDescription>
      </Alert>
    </div>
  )
}
