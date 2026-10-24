// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import type {NextCall} from './client';
import {SOON_MS, chipState, nextFlip} from './next_call';

const MIN = 60000;
const start = Date.parse('2026-10-27T14:00:00Z');
const call: NextCall = {meeting_id: 'm1', post_id: 'p1', summary: 'Weekly sync', start, end: start + 30 * MIN, live: false, recurring: false};

describe('chipState', () => {
    test('nothing without a call, or once it\'s over', () => {
        expect(chipState(null, start)).toEqual({kind: 'hidden'});
        expect(chipState(call, call.end)).toEqual({kind: 'hidden'});
        expect(chipState(call, call.end + MIN)).toEqual({kind: 'hidden'});
    });

    test('its day and time until the last hour, then the minutes left, then live', () => {
        expect(chipState(call, start - 3 * 24 * 60 * MIN)).toEqual({kind: 'upcoming'});
        expect(chipState(call, start - SOON_MS - 1)).toEqual({kind: 'upcoming'});
        expect(chipState(call, start - SOON_MS)).toEqual({kind: 'soon', minutes: 60});
        expect(chipState(call, start - 12 * MIN - 1000)).toEqual({kind: 'soon', minutes: 13});
        expect(chipState(call, start - 1000)).toEqual({kind: 'soon', minutes: 1});
        expect(chipState(call, start)).toEqual({kind: 'live'});
        expect(chipState(call, call.end - 1)).toEqual({kind: 'live'});
    });
});

describe('nextFlip', () => {
    test('wakes up when what the chip says changes, and not otherwise', () => {
        expect(nextFlip(null, start)).toBeNull();

        // Days ahead: at the start of the last hour.
        expect(nextFlip(call, start - 2 * 24 * 60 * MIN)).toBe((2 * 24 * 60 * MIN) - SOON_MS);

        // In the last hour: at each minute.
        expect(nextFlip(call, start - (12 * MIN) - 1000)).toBe(1000);
        expect(nextFlip(call, start - (12 * MIN))).toBe(MIN);

        // Live: at the end; over: never, until it's read again.
        expect(nextFlip(call, start + 10 * MIN)).toBe(20 * MIN);
        expect(nextFlip(call, call.end)).toBeNull();
    });

    test('never asks for a timer longer than setTimeout takes', () => {
        const far = {...call, start: start + 40 * 24 * 60 * MIN, end: start + 40 * 24 * 60 * MIN + MIN};
        expect(nextFlip(far, start)).toBe(2 ** 31 - 1);
    });
});
