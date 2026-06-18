import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router-dom"
import { LogOut } from "lucide-react"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { api } from "@/lib/api"
import type { User } from "@/lib/api"
import { useApiResource } from "@/hooks/useApiResource"
import { persistLanguage, type Language } from "@/i18n"
import { readStoredTheme, setTheme, type Theme } from "@/lib/theme"

// SettingsPage (§13.8.18): appearance (theme + language) and account info.
// Language and theme are the live proof that i18n + theming are wired — both
// apply immediately and persist across reloads.
export function SettingsPage({ onSignedOut }: { onSignedOut?: () => void }) {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const { data: user } = useApiResource<User>(() => api.me(), { errorFallback: "" })

  function changeLanguage(lang: Language) {
    void i18n.changeLanguage(lang)
    persistLanguage(lang)
  }

  async function handleLogout() {
    try {
      await api.logout()
    } catch {
      // Force a clean client state even if the server rejects.
    }
    onSignedOut?.()
    navigate("/login")
  }

  const currentLang = (i18n.resolvedLanguage ?? i18n.language) as Language

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <h1 className="text-2xl font-semibold tracking-tight">{t("settings.title")}</h1>

      <Card>
        <CardHeader>
          <CardTitle className="text-lg">{t("settings.appearance")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <Row label={t("settings.theme")}>
            <Select defaultValue={readStoredTheme()} onValueChange={(v) => setTheme(v as Theme)}>
              <SelectTrigger className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="light">{t("settings.themeLight")}</SelectItem>
                <SelectItem value="dark">{t("settings.themeDark")}</SelectItem>
                <SelectItem value="system">{t("settings.themeSystem")}</SelectItem>
              </SelectContent>
            </Select>
          </Row>
          <Row label={t("settings.language")}>
            <Select value={currentLang} onValueChange={(v) => changeLanguage(v as Language)}>
              <SelectTrigger className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="zh-CN">{t("settings.languageZh")}</SelectItem>
                <SelectItem value="en">{t("settings.languageEn")}</SelectItem>
              </SelectContent>
            </Select>
          </Row>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-lg">{t("settings.account")}</CardTitle>
          <CardDescription>{user?.username ?? ""}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          {user ? (
            <>
              <Row label={t("settings.username")}>
                <span>{user.username}</span>
              </Row>
              <Row label={t("settings.userId")}>
                <span className="font-mono text-xs">{user.user_id}</span>
              </Row>
              <Row label={t("settings.sessionExpires")}>
                <span>{new Date(user.expires_at).toLocaleString()}</span>
              </Row>
            </>
          ) : (
            <Skeleton className="h-16 w-full" />
          )}
          <Button variant="outline" size="sm" onClick={handleLogout}>
            <LogOut className="size-4" aria-hidden />
            {t("settings.signOut")}
          </Button>
        </CardContent>
      </Card>
    </div>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <span className="text-muted-foreground text-sm">{label}</span>
      {children}
    </div>
  )
}
