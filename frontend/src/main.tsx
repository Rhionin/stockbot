import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import '@mantine/core/styles.css'
import '@mantine/notifications/styles.css'
import './index.css'
import App from './App.tsx'
import { cssVariablesResolver, theme } from './theme'
import { installBrowserTelemetry } from './telemetry/client'

installBrowserTelemetry()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <MantineProvider theme={theme} cssVariablesResolver={cssVariablesResolver} defaultColorScheme="auto">
      <Notifications pauseResetOnHover="notification" />
      <App />
    </MantineProvider>
  </StrictMode>,
)
