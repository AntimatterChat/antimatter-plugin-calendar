// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import type {Meeting} from './client';

// The channel menu action and the meeting post's "Edit" button open the scheduling dialog this
// way; its root component (components/schedule_call_dialog.tsx) is registered once and listens
// for it. There's no Redux store of this app's own to hold "which dialog is open" in.

export type ScheduleCallTarget = {
    channelId: string;

    // The meeting to edit, absent to schedule a new one.
    meeting?: Meeting;
};

type Listener = (target: ScheduleCallTarget | null) => void;

let listeners: Listener[] = [];

// openScheduleCallDialog opens the "Schedule a call" dialog for a channel, empty for a new
// meeting, or filled in to edit an existing one.
export function openScheduleCallDialog(target: ScheduleCallTarget): void {
    for (const listener of listeners) {
        listener(target);
    }
}

// onScheduleCallDialog is called whenever the dialog should open (or close, with null).
export function onScheduleCallDialog(listener: Listener): () => void {
    listeners.push(listener);
    return () => {
        listeners = listeners.filter((l) => l !== listener);
    };
}
