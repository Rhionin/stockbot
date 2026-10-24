import { useEffect, useRef, useState } from 'react';
import type { LedgerMove } from '@stockbot/shared/types';
import {
  buildPulseSeries, pulseCount, pulseDateTicks, pulseDayFor, pulseDays, pulseEmptyCopy, pulseLegend,
  pulsePlot, pulseRanges, pulseSidePaths, pulseStartLabel, pulseSummary, pulseTickLabel, pulseYTicks,
  type PlotPoint, type PulseDay, type PulseRange, type PulseSample,
} from './pulse';

const { width: plotWidth, height: plotHeight, baseline, amplitude } = pulsePlot;

export interface PulseInspect {
  key: string;
}

interface InspectState {
  ratio: number;
  day: PulseDay;
  locked: boolean;
}

export function PulseChart({
  moves, now, setupAt, onInspect,
}: {
  moves: LedgerMove[];
  now: Date;
  setupAt?: string;
  onInspect?: (day: PulseInspect | null) => void;
}) {
  const [range, setRange] = useState<PulseRange>(30);
  const [inspect, setInspect] = useState<{ state: InspectState; signature: string } | null>(null);
  const figureRef = useRef<HTMLElement>(null);
  const hitRef = useRef<HTMLDivElement>(null);
  const gesture = useRef<{ x: number; y: number; moved: boolean; scrolling: boolean } | null>(null);
  const signature = `${range}|${setupAt ?? ''}|${moves.map((move) => `${move.id}:${move.quantity}:${move.at}`).join()}`;
  const active = inspect?.signature === signature ? inspect.state : null;

  const series = buildPulseSeries(moves, now, range, setupAt);
  const since = series.fromSetup ? pulseStartLabel(series.start, now) : undefined;
  const summary = pulseSummary({
    rangeDays: range,
    stockedIn: series.stockedIn,
    used: series.used,
    since,
  });
  const startMs = series.start.getTime();
  const endMs = series.end.getTime();
  const span = endMs - startMs || 1;
  const scale = Math.max(series.peakIn, series.peakOut);
  const stockedIn = sidePoints(series.samples, 'stockedIn', -1, startMs, span, scale);
  const used = sidePoints(series.samples, 'used', 1, startMs, span, scale);
  const above = pulseSidePaths(stockedIn, baseline);
  const below = pulseSidePaths(used, baseline);
  const days = pulseDays(moves, series.start, series.end, now);
  const dateTicks = pulseDateTicks(series.start, series.end, now);
  const yTicks = pulseYTicks(scale);

  useEffect(() => {
    const node = hitRef.current;
    if (!node) return undefined;
    let lastTap = 0;
    const end = (event: TouchEvent) => {
      const now = Date.now();
      // Android Chrome and Firefox zoom the page on a second tap this soon.
      if (now - lastTap < 400) event.preventDefault();
      lastTap = now;
    };
    node.addEventListener('touchend', end, { passive: false });
    return () => node.removeEventListener('touchend', end);
  }, []);

  useEffect(() => {
    if (!active?.locked) return undefined;
    const close = (event: PointerEvent) => {
      if (!figureRef.current?.contains(event.target as Node)) {
        setInspect(null);
        onInspect?.(null);
      }
    };
    document.addEventListener('pointerdown', close);
    return () => document.removeEventListener('pointerdown', close);
  }, [active?.locked, onInspect]);

  const showDay = (clientX: number, target: HTMLDivElement, lock: boolean) => {
    const rect = target.getBoundingClientRect();
    const ratio = Math.min(1, Math.max(0, (clientX - rect.left) / (rect.width || 1)));
    const day = pulseDayFor(days, startMs + ratio * span);
    if (!day) return;
    setInspect({ signature, state: { ratio, day, locked: lock } });
    onInspect?.({ key: day.key });
  };

  const align = tipAlign(active?.ratio ?? 0.5);

  return (
    <figure className="pulse" ref={figureRef}>
      <div className="pulse-range" role="group" aria-label="Chart range">
        {pulseRanges.map((daysOption) => (
          <button
            key={daysOption}
            type="button"
            className="pulse-range-button"
            aria-pressed={range === daysOption}
            onClick={() => {
              setRange(daysOption);
              setInspect(null);
              onInspect?.(null);
            }}
          >
            {daysOption} days
          </button>
        ))}
      </div>
      <p className="pulse-totals" aria-hidden="true">
        <span className="pulse-total is-in">{pulseCount(series.stockedIn, 'stocked in')}</span>
        <span className="pulse-total is-out">{pulseCount(series.used, 'used')}</span>
      </p>
      <div className="pulse-stage">
        <div className="pulse-y" aria-hidden="true">
          {yTicks.map((tick) => (
            <span
              key={`${tick.y}`}
              className="pulse-y-tick"
              style={{ top: `${(tick.y / plotHeight) * 100}%` }}
            >
              {pulseTickLabel(tick.value)}
            </span>
          ))}
        </div>
        <div className="pulse-canvas">
          <svg
            className="pulse-plot"
            viewBox={`0 0 ${plotWidth} ${plotHeight}`}
            preserveAspectRatio="none"
            role="img"
            aria-label={summary}
          >
            {yTicks.filter((tick) => tick.value !== 0).map((tick) => (
              <line key={`grid-${tick.y}`} className="pulse-grid" x1={0} y1={tick.y} x2={plotWidth} y2={tick.y} />
            ))}
            {above && (
              <g>
                <path className="pulse-hump" d={above.fill} fill="var(--history-in)" fillOpacity={0.5} />
                <path d={above.stroke} fill="none" stroke="var(--history-in)" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
              </g>
            )}
            {below && (
              <g>
                <path className="pulse-hump" d={below.fill} fill="var(--history-used)" fillOpacity={0.5} />
                <path d={below.stroke} fill="none" stroke="var(--history-used)" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
              </g>
            )}
            <line className="pulse-baseline" x1={0} y1={baseline} x2={plotWidth} y2={baseline} />
            {active && (
              <line
                className="pulse-crosshair"
                x1={active.ratio * plotWidth}
                x2={active.ratio * plotWidth}
                y1={8}
                y2={plotHeight - 8}
              />
            )}
          </svg>
          <div
            ref={hitRef}
            className="pulse-hit"
            onPointerDown={(event) => {
              if (event.pointerType === 'mouse' && event.button !== 0) return;
              gesture.current = { x: event.clientX, y: event.clientY, moved: false, scrolling: false };
              if (event.pointerType === 'mouse') showDay(event.clientX, event.currentTarget, false);
            }}
            onPointerMove={(event) => {
              if (event.pointerType === 'mouse') {
                showDay(event.clientX, event.currentTarget, false);
                return;
              }
              const start = gesture.current;
              if (!start || start.scrolling) return;
              const dx = event.clientX - start.x;
              const dy = event.clientY - start.y;
              if (!start.moved && Math.hypot(dx, dy) < 10) return;
              start.moved = true;
              if (Math.abs(dy) > Math.abs(dx)) {
                start.scrolling = true;
                return;
              }
              showDay(event.clientX, event.currentTarget, false);
            }}
            onPointerUp={(event) => {
              if (event.pointerType === 'mouse') return;
              const start = gesture.current;
              gesture.current = null;
              if (!start || start.scrolling) return;
              showDay(event.clientX, event.currentTarget, true);
            }}
            onPointerLeave={(event) => {
              if (event.pointerType !== 'mouse' || active?.locked) return;
              setInspect(null);
              onInspect?.(null);
            }}
            onPointerCancel={() => {
              gesture.current = null;
              if (active?.locked) return;
              setInspect(null);
              onInspect?.(null);
            }}
          />
          {active && (
            <div className="pulse-tip" role="tooltip" data-align={align} style={{ left: `${active.ratio * 100}%` }}>
              <p className="pulse-tip-date">{active.day.label}</p>
              <p className="pulse-tip-line pulse-tip-in">{pulseCount(active.day.stockedIn, 'stocked in')}</p>
              <p className="pulse-tip-line pulse-tip-out">{pulseCount(active.day.used, 'used')}</p>
            </div>
          )}
        </div>
      </div>
      <div className="pulse-dates" aria-hidden="true">
        <span />
        <div className="pulse-dates-track">
          {dateTicks.map((tick) => (
            <span
              key={`${tick.align}-${tick.at}`}
              className="pulse-date"
              data-align={tick.align}
              style={tick.align === 'center' ? { left: `${((tick.at - startMs) / span) * 100}%` } : undefined}
            >
              {tick.label}
            </span>
          ))}
        </div>
      </div>
      {series.peakIn === 0 && series.peakOut === 0 && (
        <p className="pulse-empty">{pulseEmptyCopy(range, series.fromSetup)}</p>
      )}
      {series.fromSetup && <p className="pulse-origin">The opening stock in is where this chart starts.</p>}
      <p className="pulse-legend">{pulseLegend()}</p>
    </figure>
  );
}

function tipAlign(ratio: number): 'start' | 'center' | 'end' {
  if (ratio < 0.22) return 'start';
  if (ratio > 0.78) return 'end';
  return 'center';
}

function sidePoints(
  samples: PulseSample[],
  key: 'stockedIn' | 'used',
  sign: -1 | 1,
  startMs: number,
  span: number,
  scale: number,
): PlotPoint[] {
  return samples.map((sample) => ({
    x: ((sample.at - startMs) / span) * plotWidth,
    y: scale === 0 ? baseline : baseline + sign * (sample[key] / scale) * amplitude,
  }));
}
