// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import {am, own} from '../class_names';
import type {Occurrence} from '../client';
import {durationMinutes, formatTime} from '../utils';

import Icon from './icon';
import {useMeeting} from './meeting';
import {messages} from './messages';

type Props = {
    event: Occurrence;
    color: string;
    now: boolean;
    onOpen: (event: Occurrence) => void;
};

// EventRow is an event of the agenda, as in the Fusion mockup: its time, title, details and the
// button that joins its meeting.
export default function EventRow({event, color, now, onOpen}: Props) {
    const {formatMessage, locale} = useIntl();
    const title = event.summary || formatMessage(messages.untitled);
    const meeting = useMeeting(event.link, title);

    const details: string[] = [];
    if (!event.all_day) {
        const minutes = durationMinutes(event);
        if (minutes > 0 && minutes < 60) {
            details.push(formatMessage(messages.minutes, {count: minutes}));
        } else if (minutes >= 60 && minutes % 60 === 0) {
            details.push(formatMessage(messages.hours, {count: minutes / 60}));
        } else if (minutes > 0) {
            details.push(`${formatTime(event.start, locale)}–${formatTime(event.end, locale)}`);
        }
    }
    if (event.location) {
        details.push(event.location);
    }

    return (
        <div
            className={am('event', {now})}
            style={{'--am-ev': color, '--amc-ev': color} as React.CSSProperties}
        >
            <span className={am('when')}>{event.all_day ? formatMessage(messages.allDay) : formatTime(event.start, locale)}</span>
            <button
                className={own('event-main')}
                aria-label={formatMessage(messages.edit, {title})}
                onClick={() => onOpen(event)}
            >
                <b>
                    {title}
                    {event.recurrence && (
                        <span
                            className={own('repeat')}
                            title={formatMessage(messages.repeats)}
                        >
                            <Icon
                                name='repeat'
                                size='xs'
                            />
                        </span>
                    )}
                </b>
                <span className={am('sub')}>
                    {details.join(' · ')}
                    {meeting && details.length > 0 && ' · '}
                    {meeting?.place}
                </span>
            </button>
            {meeting?.button}
        </div>
    );
}
