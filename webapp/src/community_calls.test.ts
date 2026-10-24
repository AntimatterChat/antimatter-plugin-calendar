// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import type {CommunityCall} from './client';
import {COMMUNITY_CALENDAR, COMMUNITY_COLOR, communityLayer, openPost, postPermalink, toOccurrence} from './community_calls';
import {PALETTE} from './utils';

const call: CommunityCall = {
    meeting_id: 'meeting1',
    post_id: 'post1',
    channel_id: 'channel1',
    channel_name: 'standup',
    channel_display_name: 'Standup',
    channel_type: 'P',
    organizer_id: 'organizer1',
    summary: 'Standup sync',
    start: Date.parse('2026-10-27T09:00:00Z'),
    end: Date.parse('2026-10-27T09:15:00Z'),
    recurring: true,
    my_rsvp: 'maybe',
};

describe('the "Community calls" layer', () => {
    test('is a read-only layer of its own colour, apart from the personal calendars\' palette', () => {
        const layer = communityLayer('Community calls');
        expect(layer).toEqual({id: COMMUNITY_CALENDAR, name: 'Community calls', color: COMMUNITY_COLOR, read_only: true});
        expect(PALETTE).not.toContain(COMMUNITY_COLOR);

        // A CalDAV calendar's ID is a path: it can't be mistaken for the layer.
        expect(COMMUNITY_CALENDAR.startsWith('/')).toBe(false);
    });

    test('shows each occurrence as an event of the layer, linked to its channel\'s call', () => {
        const event = toOccurrence(call);
        expect(event).toEqual(expect.objectContaining({
            calendar: COMMUNITY_CALENDAR,
            summary: 'Standup sync',
            start: call.start,
            end: call.end,
            all_day: false,
            recurrence: 'custom',
            link: {channel_id: 'channel1', kind: 'call', channel_name: 'standup', display_name: 'Standup', channel_type: 'P'},
            community: call,
        }));

        // Two occurrences of one series are two events.
        expect(toOccurrence({...call, start: call.start + 864e5}).id).not.toBe(event.id);
        expect(toOccurrence({...call, recurring: false}).recurrence).toBe('');
    });

    test('opens a call\'s post at its permalink in the community', () => {
        expect(postPermalink('lumen', 'post1')).toBe('/lumen/pl/post1');

        const push = jest.fn();
        window.WebappUtils = {browserHistory: {push}};
        openPost('lumen', 'post1');
        expect(push).toHaveBeenCalledWith('/lumen/pl/post1');

        push.mockClear();
        openPost(undefined, 'post1');
        expect(push).not.toHaveBeenCalled();
        delete window.WebappUtils;
    });
});
