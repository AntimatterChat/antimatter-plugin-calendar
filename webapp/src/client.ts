// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {Client4} from 'mattermost-redux/client';

import manifest from './manifest';

export type Account = {
    url: string;
    username: string;
    remind_minutes: number;
};

export type AccountRequest = Account & {
    password: string;
};

export type ClientConfig = {
    reminders_enabled: boolean;
    allow_insecure_connections: boolean;
};

export type Calendar = {
    id: string;
    name: string;
    color?: string;
    read_only: boolean;
    description?: string;
};

export type LinkKind = 'channel' | 'call';

export type EventLink = {
    channel_id: string;
    kind: LinkKind;
    channel_name?: string;
    display_name?: string;
    channel_type?: string;
    team_name?: string;
};

export type Recurrence = '' | 'daily' | 'weekly' | 'monthly' | 'yearly' | 'custom';

export type Occurrence = {
    id: string;
    calendar: string;
    path: string;
    etag: string;
    uid: string;
    summary: string;
    description?: string;
    location?: string;
    all_day: boolean;
    start: number;
    end: number;
    start_date?: string;
    end_date?: string;
    recurrence?: Recurrence;
    recurrence_id?: number;
    alarms?: number[];
    link?: EventLink;
};

export type EventDetails = {
    calendar: string;
    path: string;
    etag: string;
    summary: string;
    description: string;
    location: string;
    all_day: boolean;
    start: string;
    end: string;
    time_zone: string;
    recurrence: Recurrence;
    alarm: number | null;
    link?: EventLink;

    // The occurrence the details are of, for an occurrence of a repeating event
    recurrence_id?: number;
};

// Scope is what an edit of an occurrence of a repeating event applies to: the occurrence alone,
// the occurrence and the following ones, or every occurrence.
export type Scope = 'this' | 'following' | 'all';

export type EventInput = {
    calendar: string;
    summary: string;
    description: string;
    location: string;
    all_day: boolean;
    start: string;
    end: string;
    time_zone: string;
    recurrence: Recurrence;
    alarm: number | null;
    link: {channel_id: string; kind: LinkKind} | null;
};

export class ClientError extends Error {
    status: number;

    constructor(message: string, status: number) {
        super(message);
        this.status = status;
    }
}

function apiURL(path: string) {
    return `${window.basename || ''}/plugins/${manifest.id}/api/v1${path}`;
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const options: {method: string; body?: string} = {method};
    if (typeof body !== 'undefined') {
        options.body = JSON.stringify(body);
    }
    const response = await fetch(apiURL(path), Client4.getOptions(options));

    if (!response.ok) {
        let message = response.statusText;
        try {
            const data = await response.json();
            message = data.error || message;
        } catch {
            // Not JSON
        }
        throw new ClientError(message, response.status);
    }

    return response.json();
}

function query(params: Record<string, string | number | undefined>) {
    const search = new URLSearchParams();
    for (const [key, value] of Object.entries(params)) {
        if (typeof value !== 'undefined') {
            search.set(key, String(value));
        }
    }
    return search.toString();
}

// timeZone is the browser's time zone, in which events are shown and written.
export function timeZone(): string {
    return new Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
}

export function getConfig() {
    return request<ClientConfig>('GET', '/config');
}

export async function getAccount() {
    return (await request<{account: Account | null}>('GET', '/account')).account;
}

export async function saveAccount(account: AccountRequest) {
    return (await request<{account: Account}>('PUT', '/account', account)).account;
}

export function deleteAccount() {
    return request<unknown>('DELETE', '/account');
}

export function getCalendars() {
    return request<Calendar[]>('GET', '/calendars');
}

export function getEvents(from: Date, to: Date) {
    return request<{events: Occurrence[]; errors: Array<{calendar: string; error: string}>}>(
        'GET',
        `/events?${query({from: from.toISOString(), to: to.toISOString(), tz: timeZone()})}`,
    );
}

// getEvent returns an event, or one occurrence of a repeating event.
export function getEvent(path: string, occurrence?: number) {
    return request<EventDetails>('GET', `/event?${query({path, tz: timeZone(), occurrence})}`);
}

export function createEvent(input: EventInput) {
    return request<{path: string; etag: string}>('POST', '/events', input);
}

// occurrenceParams return the parameters that make a request act on an occurrence of a
// repeating event, and the following ones with the scope 'following'.
function occurrenceParams(occurrence: number, scope: Scope) {
    if (!occurrence || scope === 'all') {
        return {};
    }
    return {occurrence, scope, tz: timeZone()};
}

// updateEvent edits an event, or an occurrence of a repeating event and maybe the following ones.
export function updateEvent(path: string, etag: string, input: EventInput, occurrence = 0, scope: Scope = 'all') {
    return request<{path: string; etag: string}>('PUT', `/event?${query({path, etag, ...occurrenceParams(occurrence, scope)})}`, input);
}

// deleteEvent deletes an event, or an occurrence of a repeating event and maybe the following
// ones.
export function deleteEvent(path: string, etag: string, occurrence = 0, scope: Scope = 'this') {
    return request<unknown>('DELETE', `/event?${query({path, etag, tz: timeZone(), ...occurrenceParams(occurrence, scope)})}`);
}
