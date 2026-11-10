import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { TamaguiHarnessPage } from './TamaguiHarnessPage';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <TamaguiHarnessPage />
  </StrictMode>,
);
