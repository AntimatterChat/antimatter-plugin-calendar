// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import type {EventDetails} from '../client';

import {detailsForm, newEventForm, toInput} from './event_form';

describe('event form', () => {
    test('new events start at 9:00 on other days', () => {
        const form = newEventForm([{id: '/ro/', name: 'Holidays', read_only: true}, {id: '/work/', name: 'Work', read_only: false}], new Date(2030, 0, 15), 'channel1');
        expect(form.calendar).toBe('/work/');
        expect(form.startDate).toBe('2030-01-15');
        expect(form.startTime).toBe('09:00');
        expect(form.endTime).toBe('10:00');
        expect(form.channelId).toBe('channel1');
    });

    test('all-day events show their last day', () => {
        const details: EventDetails = {
            calendar: '/work/',
            path: '/work/x.ics',
            etag: 'e',
            summary: 'Beam time',
            description: '',
            location: '',
            all_day: true,
            start: '2026-10-01',
            end: '2026-10-03',
            time_zone: 'UTC',
            recurrence: 'weekly',
            alarm: null,
        };
        const form = detailsForm(details, 'channel1');
        expect(form.startDate).toBe('2026-10-01');
        expect(form.endDate).toBe('2026-10-02');
        expect(form.linkKind).toBe('');

        const input = toInput(form);
        expect(input.start).toBe('2026-10-01');
        expect(input.end).toBe('2026-10-03');
        expect(input.recurrence).toBe('weekly');
        expect(input.link).toBeNull();
    });

    test('timed events are sent as instants with a link', () => {
        const start = new Date(2026, 9, 5, 13, 0);
        const end = new Date(2026, 9, 5, 13, 30);
        const details: EventDetails = {
            calendar: '/work/',
            path: '/work/x.ics',
            etag: 'e',
            summary: ' Sync ',
            description: 'd',
            location: 'Lab',
            all_day: false,
            start: start.toISOString(),
            end: end.toISOString(),
            time_zone: 'Europe/Paris',
            recurrence: '',
            alarm: 10,
            link: {channel_id: 'chan', kind: 'call'},
        };
        const input = toInput(detailsForm(details, 'other'));
        expect(input.summary).toBe('Sync');
        expect(input.start).toBe(start.toISOString());
        expect(input.end).toBe(end.toISOString());
        expect(input.alarm).toBe(10);
        expect(input.link).toEqual({channel_id: 'chan', kind: 'call'});
    });
});
