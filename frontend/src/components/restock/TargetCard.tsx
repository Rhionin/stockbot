import { Button, Group } from '@mantine/core';
import type { RestockItem } from '../../restock/types';
import './restock.css';

export function TargetCard({
  item,
  onEdit,
  onUseBroader,
  onOpen,
}: {
  item: RestockItem;
  onEdit: () => void;
  onUseBroader?: () => void;
  onOpen?: () => void;
}) {
  const own = item.level === 'item';
  return (
    <div className="restock-target">
      <div className="restock-kicker">Restock target</div>
      <div className="restock-value">
        {item.valueLabel}
        {item.target.kind === 'months' && item.target.value > 0 && <span className="restock-unit"> of supply</span>}
        {item.target.kind === 'amount' && item.target.value > 0 && item.unit && <span className="restock-unit"> {item.unit}</span>}
      </div>
      <div className="restock-source">
        <span className={own ? 'restock-dot is-solid' : 'restock-dot'} />
        {own || item.level === 'household' ? <b>{item.sourceLabel}</b> : (
          <span>From <b>{item.level === 'group' ? `${item.levelName} group` : item.levelName}</b></span>
        )}
      </div>
      <div className="restock-chain">
        {item.chain.map((cell) => (
          <div key={cell.key} className={`restock-cell${cell.win ? ' is-win' : ''}${cell.struck ? ' is-struck' : ''}`}>
            <div className="k">{cell.label}</div>
            <div className="v">{cell.value}</div>
          </div>
        ))}
      </div>
      <Group className="restock-actions" gap="xs">
        <Button size="xs" variant="light" onClick={onEdit}>{item.setLabel}</Button>
        {item.useAction && onUseBroader && (
          <Button size="xs" variant="subtle" onClick={onUseBroader}>{item.useAction}</Button>
        )}
        {item.level === 'category' && onOpen && (
          <Button size="xs" variant="subtle" onClick={onOpen}>{`Open ${item.levelName}`}</Button>
        )}
      </Group>
    </div>
  );
}
