// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import type {IntlShape} from 'react-intl';

import {messages} from './components/messages';
import {addDays, dateInput, parseLocal} from './utils';

// A scheduled call's repeat rule, as the "Schedule a call" dialog edits it and the server
// validates it (recurrence.go): an RFC 5545 RRULE of the few parts the dialog offers. A rule
// with anything else in it (from another client) is kept as it is, unedited.

export type RepeatFreq = 'none' | 'daily' | 'weekly' | 'monthly';
export type RepeatEnds = 'never' | 'on' | 'after';

export type RepeatForm = {
    freq: RepeatFreq;

    // Every N days, weeks or months (1: every day, week or month).
    interval: number;

    // The days of a weekly call: 0 is Monday, 6 Sunday.
    days: number[];

    ends: RepeatEnds;

    // The last day a call may be on (YYYY-MM-DD, local), when it ends on a date.
    until: string;

    // How many calls in all, when it ends after a number of them.
    count: number;
};

// The server's bounds: at most 500 occurrences, ending at most 2 years after the first.
export const MAX_INTERVAL = 99;
export const MAX_COUNT = 500;
export const MAX_REPEAT_DAYS = 2 * 365;

const RRULE_DAYS = ['MO', 'TU', 'WE', 'TH', 'FR', 'SA', 'SU'];

// weekdayIndex returns a date's day of the week, 0 for Monday.
export function weekdayIndex(date: Date): number {
    return (date.getDay() + 6) % 7;
}

function clamp(n: number, min: number, max: number): number {
    if (!Number.isFinite(n)) {
        return min;
    }
    return Math.min(max, Math.max(min, Math.round(n)));
}

// defaultRepeat is the picker's state for a call that doesn't repeat (yet).
export function defaultRepeat(start: Date): RepeatForm {
    return {freq: 'none', interval: 1, days: [weekdayIndex(start)], ends: 'never', until: dateInput(addDays(start, 30)), count: 10};
}

// utcStamp formats an instant as an RRULE UNTIL (20261231T225959Z).
function utcStamp(date: Date): string {
    return date.toISOString().replace(/\.\d{3}/, '').replace(/[-:]/g, '');
}

// buildRRule returns the RRULE of the picker's state, '' for a call that doesn't repeat.
export function buildRRule(form: RepeatForm): string {
    if (form.freq === 'none') {
        return '';
    }
    const parts = [`FREQ=${form.freq.toUpperCase()}`];
    const interval = clamp(form.interval, 1, MAX_INTERVAL);
    if (interval > 1) {
        parts.push(`INTERVAL=${interval}`);
    }
    if (form.freq === 'weekly' && form.days.length > 0) {
        const days = [...new Set(form.days)].filter((d) => d >= 0 && d < 7).sort((a, b) => a - b);
        parts.push(`BYDAY=${days.map((d) => RRULE_DAYS[d]).join(',')}`);
    }
    if (form.ends === 'on' && form.until) {
        // The end of that day, where the person is.
        const end = parseLocal(form.until, '23:59');
        end.setSeconds(59);
        parts.push(`UNTIL=${utcStamp(end)}`);
    } else if (form.ends === 'after') {
        parts.push(`COUNT=${clamp(form.count, 1, MAX_COUNT)}`);
    }
    return parts.join(';');
}

// parseUntil reads an RRULE UNTIL (a date, or a UTC date and time) as the local day it falls on.
function parseUntil(value: string): string | null {
    const match = (/^(\d{4})(\d{2})(\d{2})(?:T(\d{2})(\d{2})(\d{2})(Z)?)?$/).exec(value);
    if (!match) {
        return null;
    }
    const [, y, mo, d, h, mi, s, z] = match;
    if (typeof h === 'undefined') {
        return `${y}-${mo}-${d}`;
    }
    const date = z ? new Date(Date.UTC(Number(y), Number(mo) - 1, Number(d), Number(h), Number(mi), Number(s))) : new Date(Number(y), Number(mo) - 1, Number(d), Number(h), Number(mi), Number(s));
    return dateInput(date);
}

// parseRRule reads a rule into the picker's state, or returns null for a rule the picker can't
// show (it's then kept as it is).
export function parseRRule(rule: string, start: Date): RepeatForm | null {
    const form = defaultRepeat(start);
    if (!rule) {
        return form;
    }
    for (const part of rule.split(';')) {
        const [key, value = ''] = part.split('=');
        switch (key.toUpperCase()) {
        case 'FREQ': {
            const freq = value.toLowerCase();
            if (freq !== 'daily' && freq !== 'weekly' && freq !== 'monthly') {
                return null;
            }
            form.freq = freq;
            break;
        }
        case 'INTERVAL': {
            const interval = Number(value);
            if (!Number.isInteger(interval) || interval < 1 || interval > MAX_INTERVAL) {
                return null;
            }
            form.interval = interval;
            break;
        }
        case 'BYDAY': {
            const days = value.split(',').map((d) => RRULE_DAYS.indexOf(d.toUpperCase()));
            if (days.some((d) => d < 0)) {
                // A position such as 1MO (the first Monday) isn't offered.
                return null;
            }
            form.days = days;
            break;
        }
        case 'UNTIL': {
            const until = parseUntil(value);
            if (!until) {
                return null;
            }
            form.ends = 'on';
            form.until = until;
            break;
        }
        case 'COUNT': {
            const count = Number(value);
            if (!Number.isInteger(count) || count < 1) {
                return null;
            }
            form.ends = 'after';
            form.count = count;
            break;
        }
        case 'WKST':
            break;
        default:
            return null;
        }
    }
    if (form.freq === 'none' || (form.freq !== 'weekly' && rule.toUpperCase().includes('BYDAY='))) {
        return null;
    }
    return form;
}

// weekdayName returns the name of a day of the week (0 for Monday) in a locale.
export function weekdayName(day: number, locale: string, style: 'long' | 'short' = 'long'): string {
    // 1 January 2024 was a Monday.
    return new Date(2024, 0, 1 + day).toLocaleDateString(locale, {weekday: style});
}

// describeRRule says a repeat rule in words ("Every week on Monday and Wednesday, until 31 Dec"),
// '' for a rule it can't (the post then shows only that the call repeats).
export function describeRRule(intl: IntlShape, rule: string, start: Date): string {
    const yearly = (/(^|;)FREQ=YEARLY(;|$)/i).test(rule);
    const form = yearly ? parseRRule(rule.replace(/FREQ=YEARLY/i, 'FREQ=MONTHLY'), start) : parseRRule(rule, start);
    if (!form || form.freq === 'none') {
        return '';
    }
    const count = form.interval;
    let words: string;
    if (yearly) {
        words = intl.formatMessage(messages.repeatWordsYearly, {count});
    } else if (form.freq === 'daily') {
        words = intl.formatMessage(messages.repeatWordsDaily, {count});
    } else if (form.freq === 'monthly') {
        words = intl.formatMessage(messages.repeatWordsMonthly, {count});
    } else {
        const days = (form.days.length ? form.days : [weekdayIndex(start)]).map((d) => weekdayName(d, intl.locale));
        words = intl.formatMessage(messages.repeatWordsWeekly, {count, days: intl.formatList(days, {type: 'conjunction'})});
    }
    if (form.ends === 'on') {
        const date = parseLocal(form.until).toLocaleDateString(intl.locale, {day: 'numeric', month: 'short', year: 'numeric'});
        return intl.formatMessage(messages.repeatWordsUntil, {rule: words, date});
    }
    if (form.ends === 'after') {
        return intl.formatMessage(messages.repeatWordsCount, {rule: words, count: form.count});
    }
    return words;
}

// The reminder choices of a call, as the server allows them (reminderChoicesMinutes): none, at
// start, 5, 10, 15 and 30 minutes, an hour, or a day before.
export const NO_REMINDER = -1;
export const REMINDER_CHOICES = [NO_REMINDER, 0, 5, 10, 15, 30, 60, 1440];
export const DEFAULT_REMINDER = 10;

// reminderLabel names a reminder choice ("10 minutes before").
export function reminderLabel(intl: IntlShape, minutes: number): string {
    if (minutes === NO_REMINDER) {
        return intl.formatMessage(messages.alarmNone);
    }
    if (minutes === 0) {
        return intl.formatMessage(messages.alarmAtStart);
    }
    if (minutes === 1440) {
        return intl.formatMessage(messages.alarmDay);
    }
    if (minutes >= 60 && minutes % 60 === 0) {
        return intl.formatMessage(messages.alarmHours, {count: minutes / 60});
    }
    return intl.formatMessage(messages.alarmMinutes, {count: minutes});
}
