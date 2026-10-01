// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import type {Calendar, Occurrence} from './client';

// The event colors of the Fusion mockup, for calendars without a color of their own
export const PALETTE = ['#8B5CF6', '#22D3EE', '#4C9AFF', '#23A55A', '#F472B6', '#F0B232', '#F23F43'];

export function calendarColor(calendars: Calendar[], id: string): string {
    const index = calendars.findIndex((c) => c.id === id);
    if (index < 0) {
        return PALETTE[0];
    }
    return calendars[index].color || PALETTE[index % PALETTE.length];
}

export function startOfDay(date: Date): Date {
    const d = new Date(date);
    d.setHours(0, 0, 0, 0);
    return d;
}

export function addDays(date: Date, days: number): Date {
    const d = new Date(date);
    d.setDate(d.getDate() + days);
    return d;
}

// startOfWeek returns the Monday of the week of a date.
export function startOfWeek(date: Date): Date {
    const d = startOfDay(date);
    const offset = (d.getDay() + 6) % 7;
    return addDays(d, -offset);
}

export function sameDay(a: Date, b: Date) {
    return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

function pad(n: number) {
    return String(n).padStart(2, '0');
}

// dateInput formats a date for <input type="date">, in local time.
export function dateInput(date: Date): string {
    return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

// timeInput formats a time for <input type="time">, in local time.
export function timeInput(date: Date): string {
    return `${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

// parseLocal reads the values of date and time inputs, in local time.
export function parseLocal(date: string, time = '00:00'): Date {
    const [y, m, d] = date.split('-').map(Number);
    const [hh, mm] = time.split(':').map(Number);
    return new Date(y, (m || 1) - 1, d || 1, hh || 0, mm || 0);
}

export function formatTime(ms: number, locale?: string): string {
    return new Date(ms).toLocaleTimeString(locale, {hour: '2-digit', minute: '2-digit'});
}

// occursOn returns whether an occurrence takes place on a day (all-day events by their dates).
export function occursOn(o: Occurrence, day: Date): boolean {
    const dayStart = startOfDay(day).getTime();
    const dayEnd = addDays(startOfDay(day), 1).getTime();
    if (o.all_day && o.start_date && o.end_date) {
        const date = dateInput(day);
        return o.start_date <= date && date < o.end_date;
    }
    if (o.start === o.end) {
        return o.start >= dayStart && o.start < dayEnd;
    }
    return o.start < dayEnd && o.end > dayStart;
}

// eventsOn returns the occurrences of a day, all-day events first.
export function eventsOn(events: Occurrence[], day: Date): Occurrence[] {
    return events.filter((o) => occursOn(o, day)).sort((a, b) => {
        if (a.all_day !== b.all_day) {
            return a.all_day ? -1 : 1;
        }
        return a.start - b.start;
    });
}

export function isNow(o: Occurrence, now: number): boolean {
    return !o.all_day && o.start <= now && now < Math.max(o.end, o.start + 1);
}

export function durationMinutes(o: Occurrence): number {
    return Math.round((o.end - o.start) / 60000);
}
