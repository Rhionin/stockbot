import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Group, Modal, Text, TextInput } from '@mantine/core';
import { CHANGEOVER_BANNER, MAKE_MY_ACCOUNT } from '@stockbot/shared/contract';

export function ChangeoverBanner() {
  const navigate = useNavigate();
  const [opened, setOpened] = useState(false);
  const [code, setCode] = useState('');

  return (
    <div className="changeover-banner" role="status">
      <p>{CHANGEOVER_BANNER}</p>
      <Button size="compact-sm" variant="white" color="teal" onClick={() => setOpened(true)}>
        {MAKE_MY_ACCOUNT}
      </Button>
      <Modal opened={opened} onClose={() => setOpened(false)} title={MAKE_MY_ACCOUNT}>
        <Text>An owner sends you an invite. Paste the code from that link.</Text>
        <TextInput label="Invite code" value={code} onChange={(event) => setCode(event.currentTarget.value)} mt="sm" />
        <Group justify="flex-end" mt="md">
          <Button variant="default" onClick={() => setOpened(false)}>Cancel</Button>
          <Button
            disabled={code.trim() === ''}
            onClick={() => navigate(`/invite/${encodeURIComponent(code.trim())}`)}
          >
            Continue
          </Button>
        </Group>
      </Modal>
    </div>
  );
}
