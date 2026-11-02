import { h } from '../dom.js';
import { formatCount } from './count-formatters.js';
import { effect } from '../reactive.js';
import { renderReactiveGraphWidget } from './graph-widget.js';
import { createDebug } from '../debug.js';

const debugFactoryRhythm = createDebug('factory-rhythm');

/** @typedef {{ label: string, date: string, count: number, previous: number, reached: boolean }} RhythmDay */
/** @typedef {{ rows: () => Record<string, unknown>[], pending?: () => boolean }} RhythmSource */
/** @typedef {{ signal: AbortSignal }} ReactiveScope */

/**
 * @param {RhythmSource} source
 * @param {ReactiveScope} scope
 */
export function renderFactoryRhythm(source, scope) {
  /** @type {boolean | undefined} */
  let wasPending;
  debugFactoryRhythm({ event: 'composed' });
  const rendered = renderReactiveGraphWidget({
    className: 'factory-rhythm',
    headingClassName: 'factory-rhythm-heading',
    legendClassName: 'factory-rhythm-legend',
    plotClassName: 'factory-rhythm-bars',
    title: 'Weekly rhythm',
    subtitle: 'Agentic Workflow runs',
    ariaLabel: 'Agentic Workflow runs this week and last week from Monday through Sunday',
    legendLabel: 'Weekly rhythm legend',
    legend: [
      { label: 'This week', className: 'factory-rhythm-legend-current' },
      { label: 'Last week', className: 'factory-rhythm-legend-previous' }
    ],
    items: () => {
      const days = rhythmPayload(source).days;
      const maximum = Math.max(...days.flatMap((day) => [day.count, day.previous]), 1);
      return days.map((day) => ({
        ...day,
        currentHeight: day.count > 0 ? Math.max(5, day.count / maximum * 100) : 0,
        previousHeight: day.previous > 0 ? Math.max(5, day.previous / maximum * 100) : 0
      }));
    },
    key: (day) => day.label,
    renderItem: () => createRhythmDay(),
    updateItem: (element, day) => {
      const description = rhythmDayDescription(day);
      element.classList.toggle('factory-rhythm-day-future', !day.reached);
      element.setAttribute('aria-label', description);
      element.title = description;
      const current = element.querySelector('.factory-rhythm-current');
      if (current instanceof HTMLElement) {
        current.hidden = day.count <= 0;
        current.style.height = `${day.currentHeight}%`;
      }
      const baseline = element.querySelector('.factory-rhythm-baseline');
      if (baseline instanceof HTMLElement) {
        baseline.hidden = day.previous <= 0;
        baseline.style.height = `${day.previousHeight}%`;
      }
      const label = element.querySelector('small');
      if (label) label.textContent = day.label;
    },
    signal: scope.signal
  });
  effect(() => {
    const pending = source.pending?.() ?? false;
    rendered.classList.toggle('factory-rhythm-pending', pending);
    rendered.toggleAttribute('aria-busy', pending);
    if (pending !== wasPending) {
      wasPending = pending;
      debugFactoryRhythm({ event: 'pending-changed', pending });
    }
  }, { signal: scope.signal });
  return rendered;
}

/** @param {RhythmDay} day */
function rhythmDayDescription(day) {
  const counts = [];
  if (day.count > 0) counts.push(`${formatCount(day.count)} workflow ${day.count === 1 ? 'run' : 'runs'} this week`);
  if (day.previous > 0) counts.push(`${formatCount(day.previous)} workflow ${day.previous === 1 ? 'run' : 'runs'} last week`);
  const summary = counts.length ? counts.join('; ') : 'No workflow runs this week or last week';
  return `${day.label}${day.date ? ` ${day.date}` : ''}: ${summary}${day.reached ? '' : ' (day not yet reached)'}.`;
}

/** @param {RhythmSource} source @returns {{ days: RhythmDay[] }} */
function rhythmPayload(source) {
  const row = source.rows()[0];
  const value = row?.rhythm;
  const configured = value && typeof value === 'object' && !Array.isArray(value)
    ? /** @type {Record<string, unknown>} */ (value)
    : {};
  const input = Array.isArray(configured.days) ? configured.days : [];
  const days = input.flatMap((candidate) => {
    if (!candidate || typeof candidate !== 'object' || Array.isArray(candidate)) return [];
    const day = /** @type {Record<string, unknown>} */ (candidate);
    if (typeof day.label !== 'string' || typeof day.date !== 'string' || typeof day.reached !== 'boolean') return [];
    return [{
      label: day.label,
      date: day.date,
      count: numberField(day, 'current'),
      previous: numberField(day, 'previous'),
      reached: day.reached
    }];
  });
  if (days.length !== 7) {
    debugFactoryRhythm({ event: 'payload-fallback', receivedDayCount: days.length });
  }
  return {
    days: days.length === 7
      ? days
      : ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'].map((label) => ({
          label,
          date: '',
          count: 0,
          previous: 0,
          reached: false
        }))
  };
}

/** @param {Record<string, unknown>} row @param {string} field */
function numberField(row, field) {
  const value = Number(row[field]);
  return Number.isFinite(value) ? value : 0;
}

function createRhythmDay() {
  return h(
    'div',
    {
      className: 'factory-rhythm-day',
      role: 'img'
    },
    h('span', { className: 'factory-rhythm-bar-pair', 'aria-hidden': 'true' },
      h('i', { className: 'factory-rhythm-baseline' }),
      h('i', { className: 'factory-rhythm-current' })
    ),
    h('small', {})
  );
}