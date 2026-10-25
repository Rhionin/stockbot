import { Select } from '@mantine/core';
import { useNavigate } from 'react-router-dom';
import { switchHousehold } from '../../api/client';
import { useHouseholdSession } from '../auth/householdSession';
import { announceHouseholdSwitch } from './liveHints';

export function HouseholdSwitcher() {
  const navigate = useNavigate();
  const { auth, replaceAuth } = useHouseholdSession();
  if (auth === null || auth.kind === 'signed_out') return null;
  const options = auth.households.map((household) => ({
    value: household.id,
    label: household.deletedAt ? `${household.name} (deleted)` : household.name,
  }));
  if (options.length === 0) {
    return (
      <button type="button" className="household-switch-link" onClick={() => navigate('/household')}>
        Choose a household
      </button>
    );
  }
  if (options.length === 1 && auth.activeHouseholdId) {
    return <span className="household-switch-name">{options[0]?.label}</span>;
  }
  return (
    <Select
      className="household-switch"
      aria-label="Household"
      data={options}
      value={auth.activeHouseholdId}
      allowDeselect={false}
      onChange={(value) => {
        if (!value || value === auth.activeHouseholdId) return;
        void switchHousehold(value).then((session) => {
          replaceAuth(session);
          announceHouseholdSwitch();
        });
      }}
    />
  );
}
