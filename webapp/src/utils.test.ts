// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import type {Calendar, Occurrence} from './client';
import {PALETTE, addDays, calendarColor, dateInput, eventsOn, isNow, occursOn, parseLocal, startOfWeek, timeInput} from './utils';

function occurrence(fields: Partial<Occurrence>): Occurrence {
    return {id: 'x', calendar: '/c/', path: '/c/x.ics', etag: '', uid: 'x', summary: 'x', all_day: false, start: 0, end: 0, ...fields};
}

describe('dates', () => {
    test('weeks start on Monday', () => {
        const wednesday = new Date(2026, 9, 7, 15, 30);
        expect(dateInput(startOfWeek(wednesday))).toBe('2026-10-05');
        expect(dateInput(startOfWeek(new Date(2026, 9, 11)))).toBe('2026-10-05');
        expect(dateInput(startOfWeek(new Date(2026, 9, 12)))).toBe('2026-10-12');
    });

    test('input values', () => {
        const date = parseLocal('2026-10-05', '09:05');
        expect(date.getHours()).toBe(9);
        expect(dateInput(date)).toBe('2026-10-05');
        expect(timeInput(date)).toBe('09:05');
        expect(dateInput(addDays(date, 27))).toBe('2026-11-01');
    });
});

describe('occurrences', () => {
    const day = new Date(2026, 9, 5);
    const at = (h: number, m = 0) => new Date(2026, 9, 5, h, m).getTime();

    test('occursOn', () => {
        expect(occursOn(occurrence({start: at(9), end: at(10)}), day)).toBe(true);
        expect(occursOn(occurrence({start: at(23), end: at(25)}), addDays(day, 1))).toBe(true);
        expect(occursOn(occurrence({start: at(24), end: at(24)}), day)).toBe(false);
        expect(occursOn(occurrence({all_day: true, start_date: '2026-10-04', end_date: '2026-10-06'}), day)).toBe(true);
        expect(occursOn(occurrence({all_day: true, start_date: '2026-10-04', end_date: '2026-10-05'}), day)).toBe(false);
    });

    test('eventsOn sorts all-day events first', () => {
        const events = [
            occurrence({id: 'b', start: at(11), end: at(12)}),
            occurrence({id: 'a', start: at(9), end: at(10)}),
            occurrence({id: 'all', all_day: true, start_date: '2026-10-05', end_date: '2026-10-06', start: at(0)}),
            occurrence({id: 'other', start: at(33), end: at(34)}),
        ];
        expect(eventsOn(events, day).map((o) => o.id)).toEqual(['all', 'a', 'b']);
    });

    test('isNow', () => {
        expect(isNow(occurrence({start: at(9), end: at(10)}), at(9, 30))).toBe(true);
        expect(isNow(occurrence({start: at(9), end: at(10)}), at(10))).toBe(false);
        expect(isNow(occurrence({all_day: true, start: at(0), end: at(24)}), at(9))).toBe(false);
    });

    test('calendarColor', () => {
        const calendars: Calendar[] = [{id: '/a/', name: 'A', read_only: false}, {id: '/b/', name: 'B', read_only: false, color: '#123456'}];
        expect(calendarColor(calendars, '/a/')).toBe(PALETTE[0]);
        expect(calendarColor(calendars, '/b/')).toBe('#123456');
    });
});
