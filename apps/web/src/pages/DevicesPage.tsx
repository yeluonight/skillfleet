import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Rocket } from "lucide-react"

import { Button } from "@/components/ui/button"
import { DevicesCard } from "@/components/DevicesCard"
import { DevicesList } from "@/components/DevicesList"
import { EnrollWizard } from "@/components/EnrollWizard"

// DevicesPage is the §13.8 Devices route: a guided-enrollment entry point, the
// enrollment-token card, then the enrolled-device list. Each device row
// expands in place to its skill roots + inventory matrix (the matrix is a
// per-device subview, not a fleet-wide table — §13.8 keeps the full matrix off
// the dashboard).
export function DevicesPage() {
  const { t } = useTranslation()
  const [wizardOpen, setWizardOpen] = useState(false)
  return (
    <div className="mx-auto max-w-4xl space-y-6">
      <div className="flex items-center justify-between gap-4">
        <h1 className="text-2xl font-semibold tracking-tight">{t("nav.devices")}</h1>
        <Button onClick={() => setWizardOpen(true)}>
          <Rocket className="size-4" aria-hidden />
          {t("enroll.startWizard")}
        </Button>
      </div>
      <DevicesCard />
      <DevicesList />
      <EnrollWizard open={wizardOpen} onOpenChange={setWizardOpen} />
    </div>
  )
}
