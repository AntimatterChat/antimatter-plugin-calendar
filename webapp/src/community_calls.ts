// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import type {Calendar, CommunityCall, Occurrence} from './client';

// The "Community calls" layer of the calendar panel: the calls scheduled in the community's
// channels the person may read (the server decides which: GET /meetings), shown with the events
// of their own calendars and toggled beside them. It needs no calendar account.

// COMMUNITY_CALENDAR is the layer's ID among the calendars (it can't be a CalDAV path, which
// starts with a slash), also kept in the hidden calendars when it's toggled off.
export const COMMUNITY_CALENDAR = '@community';

// The layer's own colour, apart from the palette personal calendars take theirs from.
export const COMMUNITY_COLOR = '#14B8A6';

export function communityLayer(name: string): Calendar {
    return {id: COMMUNITY_CALENDAR, name, color: COMMUNITY_COLOR, read_only: true};
}

// toOccurrence shows an occurrence of a community call as an event of the layer, linked to its
// channel's call (so its row can offer Join while it's on).
export function toOccurrence(call: CommunityCall): Occurrence {
    return {
        id: `${COMMUNITY_CALENDAR}:${call.meeting_id}:${call.start}`,
        calendar: COMMUNITY_CALENDAR,
        path: '',
        etag: '',
        uid: call.meeting_id,
        summary: call.summary,
        all_day: false,
        start: call.start,
        end: call.end,
        recurrence: call.recurring ? 'custom' : '',
        link: {
            channel_id: call.channel_id,
            kind: 'call',
            channel_name: call.channel_name,
            display_name: call.channel_display_name,
            channel_type: call.channel_type,
        },
        community: call,
    };
}

// postPermalink is the address of a post in a community.
export function postPermalink(teamName: string, postId: string): string {
    return `/${teamName}/pl/${postId}`;
}

// openPost opens a post where it is, in its channel.
export function openPost(teamName: string | undefined, postId: string): void {
    if (teamName && postId) {
        window.WebappUtils?.browserHistory.push(postPermalink(teamName, postId));
    }
}
