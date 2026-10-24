// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

// The server tells open clients a meeting changed (another member RSVP'd, or the organiser
// edited or cancelled it) over its own WebSocket event, registered once in index.tsx. Every
// mounted meeting post reads the meeting again when it's theirs.

type Listener = (meetingId: string) => void;

let listeners: Listener[] = [];

export function meetingChanged(meetingId: string): void {
    for (const listener of listeners) {
        listener(meetingId);
    }
}

export function onMeetingChanged(listener: Listener): () => void {
    listeners.push(listener);
    return () => {
        listeners = listeners.filter((l) => l !== listener);
    };
}
