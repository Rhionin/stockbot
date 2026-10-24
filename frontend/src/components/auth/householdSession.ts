import { createContext, useContext } from 'react';
import type { AuthSession } from '@stockbot/shared/contract';

export interface HouseholdSessionValue {
  required: boolean;
  logout: () => void;
  auth: AuthSession | null;
  replaceAuth: (session: AuthSession) => void;
}

export const HouseholdSessionContext = createContext<HouseholdSessionValue>({
  required: false,
  logout: () => {},
  auth: null,
  replaceAuth: () => {},
});

export const useHouseholdSession = () => useContext(HouseholdSessionContext);
