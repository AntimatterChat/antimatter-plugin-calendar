// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import * as client from '../client';
import type {Calendar, EventDetails, EventInput, LinkKind, Recurrence} from '../client';
import {addDays, dateInput, parseLocal, sameDay, timeInput} from '../utils';

// Form holds the fields of the event editor.
export type Form = {
    calendar: string;
    summary: string;
    description: string;
    location: string;
    allDay: boolean;
    startDate: string;
    startTime: string;
    endDate: string;
    endTime: string;
    recurrence: Recurrence;
    alarm: number | null;
    linkKind: '' | LinkKind;
    channelId: string;
};

// newEventForm starts a new event: on the next hour today, at 9:00 on other days.
export function newEventForm(calendars: Calendar[], day: Date, channelId: string): Form {
    const now = new Date();
    let start = parseLocal(dateInput(day), '09:00');
    if (sameDay(day, now)) {
        start = new Date(now);
        start.setHours(now.getHours() + 1, 0, 0, 0);
    }
    const end = new Date(start.getTime() + (60 * 60 * 1000));
    return {
        calendar: calendars.find((c) => !c.read_only)?.id || '',
        summary: '',
        description: '',
        location: '',
        allDay: false,
        startDate: dateInput(start),
        startTime: timeInput(start),
        endDate: dateInput(end),
        endTime: timeInput(end),
        recurrence: '',
        alarm: null,
        linkKind: '',
        channelId,
    };
}

export function detailsForm(details: EventDetails, channelId: string): Form {
    let start: Date;
    let end: Date;
    if (details.all_day) {
        start = parseLocal(details.start);

        // The editor shows the last day; iCalendar ends all-day events the day after
        end = addDays(parseLocal(details.end), -1);
    } else {
        start = new Date(details.start);
        end = new Date(details.end);
    }
    return {
        calendar: details.calendar,
        summary: details.summary,
        description: details.description,
        location: details.location,
        allDay: details.all_day,
        startDate: dateInput(start),
        startTime: timeInput(start),
        endDate: dateInput(end < start ? start : end),
        endTime: timeInput(end),
        recurrence: details.recurrence,
        alarm: details.alarm,
        linkKind: details.link?.kind || '',
        channelId: details.link?.channel_id || channelId,
    };
}

export function toInput(form: Form): EventInput {
    let start: string;
    let end: string;
    if (form.allDay) {
        start = form.startDate;
        end = dateInput(addDays(parseLocal(form.endDate < form.startDate ? form.startDate : form.endDate), 1));
    } else {
        start = parseLocal(form.startDate, form.startTime).toISOString();
        end = parseLocal(form.endDate, form.endTime).toISOString();
    }
    return {
        calendar: form.calendar,
        summary: form.summary.trim(),
        description: form.description,
        location: form.location,
        all_day: form.allDay,
        start,
        end,
        time_zone: client.timeZone(),
        recurrence: form.recurrence,
        alarm: form.alarm,
        link: form.linkKind && form.channelId ? {channel_id: form.channelId, kind: form.linkKind} : null,
    };
}
