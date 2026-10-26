import { Select } from '@mantine/core';
import { useNavigate } from 'react-router-dom';
import { switchHousehold } from '../../api/client';
import { useHouseholdSession } from '../auth/householdSession';
import { announceHouseholdSwitch } from './liveHints';

const OTHER_HOUSEHOLDS_VALUE = '__other_households__';

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
      <button type="button" className="household-switch-link" onClick={() => navigate('/households')}>
        Choose a household
      </button>
    );
  }
  if (options.length === 1 && auth.activeHouseholdId) {
    return (
      <button type="button" className="household-switch-name" onClick={() => navigate('/households')}>
        {options[0]?.label}
      </button>
    );
  }
  const data = [...options, { value: OTHER_HOUSEHOLDS_VALUE, label: 'Other households…' }];
  return (
    <Select
      className="household-switch"
      aria-label="Household"
      data={data}
      value={auth.activeHouseholdId}
      allowDeselect={false}
      onChange={(value) => {
        if (!value) return;
        if (value === OTHER_HOUSEHOLDS_VALUE) {
          navigate('/households');
          return;
        }
        if (value === auth.activeHouseholdId) return;
        void switchHousehold(value)
          .then((session) => {
            replaceAuth(session);
            announceHouseholdSwitch();
          })
          .catch(() => {});
      }}
    />
  );
}
