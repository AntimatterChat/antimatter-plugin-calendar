// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import {am, own} from '../class_names';
import type {Calendar, Occurrence} from '../client';
import {addDays, calendarColor, eventsOn, isNow} from '../utils';

import EventRow from './event_row';
import {messages} from './messages';

// UPCOMING_DAYS is the number of days after the selected one the agenda shows.
export const UPCOMING_DAYS = 6;

type Props = {
    day: Date;
    events: Occurrence[];
    calendars: Calendar[];
    now: number;
    onOpen: (event: Occurrence) => void;
};

function DayEvents({events, calendars, now, onOpen}: {events: Occurrence[]; calendars: Calendar[]; now: number; onOpen: (event: Occurrence) => void}) {
    return (
        <div className={am('agenda')}>
            {events.map((event) => (
                <EventRow
                    key={event.id}
                    event={event}
                    color={calendarColor(calendars, event.calendar)}
                    now={isNow(event, now)}
                    onOpen={onOpen}
                />
            ))}
        </div>
    );
}

// Agenda shows the events of a day, as in the Fusion mockup, and those of the following days.
export default function Agenda({day, events, calendars, now, onOpen}: Props) {
    const {formatMessage, locale} = useIntl();
    const today = eventsOn(events, day);
    const upcoming = Array.from({length: UPCOMING_DAYS}, (_, i) => addDays(day, i + 1)).
        map((d) => ({day: d, events: eventsOn(events, d)})).
        filter((d) => d.events.length > 0);

    return (
        <>
            <div className={am('cal-day')}>
                <b>{day.toLocaleDateString(locale, {weekday: 'long'})}</b>
                <span>{day.toLocaleDateString(locale, {month: 'long', day: 'numeric'})}</span>
            </div>
            {today.length === 0 ? (
                <div className={am('empty')}>{formatMessage(messages.noEvents)}</div>
            ) : (
                <DayEvents
                    events={today}
                    calendars={calendars}
                    now={now}
                    onOpen={onOpen}
                />
            )}
            {upcoming.length > 0 && (
                <div className={am('app-h')}>
                    <span className={am('grow')}>{formatMessage(messages.upcoming)}</span>
                </div>
            )}
            {upcoming.map((d) => (
                <React.Fragment key={d.day.getTime()}>
                    <div className={own('day-h')}>{d.day.toLocaleDateString(locale, {weekday: 'long', month: 'long', day: 'numeric'})}</div>
                    <DayEvents
                        events={d.events}
                        calendars={calendars}
                        now={now}
                        onOpen={onOpen}
                    />
                </React.Fragment>
            ))}
        </>
    );
}
