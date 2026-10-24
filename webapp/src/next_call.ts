// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import type {NextCall} from './client';

// The channel header's next call (components/next_call_chip.tsx): what it says when, and when that
// changes. The server picks the call (GET /channels/{id}/meetings/next: live, or the next one
// within 7 days); the chip flips on its own timer from there, reading the server again only when
// something changed, when the call is over, and every REFRESH_MS at most.

// In the last SOON_MS before the call starts, the chip counts down the minutes.
export const SOON_MS = 60 * 60000;

// How often the chip reads the next call again anyway (a call coming into the 7 days): never
// faster than MIN_FETCH_GAP_MS.
export const REFRESH_MS = 15 * 60000;
export const MIN_FETCH_GAP_MS = 60000;

// setTimeout's longest delay; the chip only ever waits up to 7 days, well within it.
const MAX_DELAY = (2 ** 31) - 1;

export type ChipState =
    {kind: 'hidden'} |
    {kind: 'upcoming'} |
    {kind: 'soon'; minutes: number} |
    {kind: 'live'};

// chipState is what the chip says at now: nothing, the call's day and time, "in N min" in the
// last hour, or "Live" while it's on.
export function chipState(next: NextCall | null, now: number): ChipState {
    if (!next || now >= next.end) {
        return {kind: 'hidden'};
    }
    if (now >= next.start) {
        return {kind: 'live'};
    }
    const left = next.start - now;
    if (left <= SOON_MS) {
        return {kind: 'soon', minutes: Math.max(1, Math.ceil(left / 60000))};
    }
    return {kind: 'upcoming'};
}

// nextFlip returns in how many milliseconds what the chip says next changes: the start of the last
// hour, each minute of it, the start, and the end. null once the call is over (or there's none).
export function nextFlip(next: NextCall | null, now: number): number | null {
    if (!next || now >= next.end) {
        return null;
    }
    if (now >= next.start) {
        return Math.min(next.end - now, MAX_DELAY);
    }
    const left = next.start - now;
    if (left > SOON_MS) {
        return Math.min(left - SOON_MS, MAX_DELAY);
    }
    const toMinute = left % 60000;
    return toMinute === 0 ? 60000 : toMinute;
}
