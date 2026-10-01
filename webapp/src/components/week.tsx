// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import {own} from '../class_names';
import type {Calendar, Occurrence} from '../client';
import {addDays, calendarColor, eventsOn, formatTime, isNow, sameDay} from '../utils';

import {messages} from './messages';

type Props = {
    weekStart: Date;
    events: Occurrence[];
    calendars: Calendar[];
    now: number;
    onOpen: (event: Occurrence) => void;
    onDay: (day: Date) => void;
};

// Week shows the events of a week, a column per day.
export default function Week({weekStart, events, calendars, now, onOpen, onDay}: Props) {
    const {formatMessage, locale} = useIntl();
    const days = Array.from({length: 7}, (_, i) => addDays(weekStart, i));
    const today = new Date(now);

    return (
        <div
            className={own('week')}
            role='grid'
        >
            {days.map((day) => (
                <div
                    key={day.getTime()}
                    className={own('week-day', {today: sameDay(day, today)})}
                    role='gridcell'
                >
                    <button
                        className={own('week-head')}
                        onClick={() => onDay(day)}
                    >
                        <span>{day.toLocaleDateString(locale, {weekday: 'short'})}</span>
                        <b>{day.getDate()}</b>
                    </button>
                    {eventsOn(events, day).map((event) => (
                        <button
                            key={event.id}
                            className={own('chip', {now: isNow(event, now), 'all-day': event.all_day})}
                            style={{'--amc-ev': calendarColor(calendars, event.calendar)} as React.CSSProperties}
                            title={event.summary}
                            onClick={() => onOpen(event)}
                        >
                            <span className={own('chip-time')}>{event.all_day ? formatMessage(messages.allDay) : formatTime(event.start, locale)}</span>
                            <span className={own('chip-title')}>{event.summary || formatMessage(messages.untitled)}</span>
                        </button>
                    ))}
                </div>
            ))}
        </div>
    );
}
