import { useState, type FormEvent } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router-dom"
import { Rocket, AlertCircle } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert"

import { api, ApiError } from "@/lib/api"

// SetupPage is the one-time admin bootstrap. The operator pastes the
// SF-SETUP-…-… code that the server printed to stderr on first boot.
// After a successful consume the page hands off to LoginPage; we don't
// auto-login because the setup response doesn't carry a session
// cookie (POST /api/setup is intentionally narrower than /api/login).
export function SetupPage({ onSetupComplete }: { onSetupComplete: () => void }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [code, setCode] = useState("")
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    setPending(true)
    setError(null)
    try {
      await api.setup(code, username, password)
      onSetupComplete()
      navigate("/login")
    } catch (err) {
      if (err instanceof ApiError) {
        switch (err.code) {
          case "already_consumed":
          case "no_pending_setup":
            setError(t("setup.alreadyComplete"))
            break
          case "code_mismatch":
            setError(t("setup.codeMismatch"))
            break
          default:
            setError(err.message)
        }
      } else {
        setError(t("common.networkError"))
      }
    } finally {
      setPending(false)
    }
  }

  return (
    <main className="bg-background flex min-h-svh items-center justify-center px-4 py-12">
      <Card className="w-full max-w-md">
        <CardHeader className="space-y-1">
          <CardTitle className="flex items-center gap-2 text-2xl">
            <Rocket className="text-primary size-5" aria-hidden />
            {t("setup.title")}
          </CardTitle>
          <CardDescription>{t("setup.description")}</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={onSubmit} className="space-y-4" noValidate>
            <div className="space-y-2">
              <Label htmlFor="setup-code">{t("setup.code")}</Label>
              <Input
                id="setup-code"
                name="code"
                placeholder={t("setup.codePlaceholder")}
                autoFocus
                autoComplete="off"
                spellCheck={false}
                value={code}
                onChange={(e) => setCode(e.target.value)}
                disabled={pending}
                required
                className="font-mono"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="setup-username">{t("auth.username")}</Label>
              <Input
                id="setup-username"
                name="username"
                autoComplete="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                disabled={pending}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="setup-password">{t("auth.password")}</Label>
              <Input
                id="setup-password"
                name="password"
                type="password"
                autoComplete="new-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={pending}
                required
                minLength={12}
              />
              <p className="text-muted-foreground text-xs">{t("setup.passwordHint")}</p>
            </div>
            {error && (
              <Alert variant="destructive">
                <AlertCircle className="size-4" aria-hidden />
                <AlertTitle>{t("setup.failed")}</AlertTitle>
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
            <Button
              type="submit"
              className="w-full"
              disabled={pending || !code || !username || password.length < 12}
            >
              {pending ? t("setup.creatingAdmin") : t("setup.createAdmin")}
            </Button>
          </form>
        </CardContent>
      </Card>
    </main>
  )
}
