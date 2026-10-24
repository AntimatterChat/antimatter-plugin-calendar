// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import {useIntl} from 'react-intl';
import {useSelector} from 'react-redux';

import type {Post} from '@mattermost/types/posts';
import type {GlobalState} from '@mattermost/types/store';

import {getChannel} from 'mattermost-redux/selectors/entities/channels';
import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {am, own} from '../class_names';
import * as client from '../client';
import type {CancelScope, Meeting, RSVPStatus} from '../client';
import {onMeetingChanged} from '../meeting_events';
import {DEFAULT_REMINDER, REMINDER_CHOICES, describeRRule, reminderLabel} from '../recurrence';
import {openScheduleCallDialog} from '../schedule_call';
import {formatTime} from '../utils';

import {confirmDialog} from './fusion/confirm_dialog';
import {useMeeting} from './meeting';
import {messages} from './messages';

type Props = {post: Post};

// meetingIdOf reads the meeting's ID off the post's props, carefully: props come over the wire
// and aren't typed.
function meetingIdOf(post: Post): string {
    const id = post.props?.meeting_id;
    return typeof id === 'string' ? id : '';
}

const RSVP_CHOICES: RSVPStatus[] = ['going', 'maybe', 'no'];

// The value of the "Remind me" choice that follows the call's own reminder.
const REMIND_DEFAULT = 'default';

// MeetingPost is the post of a call scheduled for the channel (registerPostTypeComponent
// 'custom_calendar_meeting'): its time (the next one, and how it repeats, for a series), a Join
// button when Calls or Voice channels can open it, "Add to calendar", RSVP (for the series, or
// its next call only), the member's own reminder, and Edit (its organiser) and Cancel (whoever
// the server says may: can_cancel).
export default function MeetingPost({post}: Props) {
    const intl = useIntl();
    const {formatMessage, locale} = intl;
    const meetingId = meetingIdOf(post);
    const currentUserId = useSelector(getCurrentUserId);
    const channel = useSelector((state: GlobalState) => getChannel(state, post.channel_id));

    const [meeting, setMeeting] = useState<Meeting | null>(null);
    const [error, setError] = useState('');
    const [busy, setBusy] = useState(false);
    const [nextOnly, setNextOnly] = useState(false);
    const [choosingCancel, setChoosingCancel] = useState(false);

    const load = useCallback(() => {
        if (!meetingId) {
            return;
        }
        client.getMeeting(meetingId).then(setMeeting).catch((err) => setError((err as Error).message));
    }, [meetingId]);

    // Re-read after an edit (the post's own edit_at changes) and whenever the server says this
    // meeting changed (another member's RSVP, or the organiser's edit or cancellation).
    useEffect(() => {
        load();
    }, [load, post.edit_at]);

    useEffect(() => onMeetingChanged((changedId) => {
        if (changedId === meetingId) {
            load();
        }
    }), [meetingId, load]);

    // The checkbox starts as the member's last answer was: for the next call only, or not.
    const answeredNextOnly = Boolean(meeting?.my_rsvp_next_only);
    useEffect(() => {
        setNextOnly(answeredNextOnly);
    }, [answeredNextOnly]);

    const title = meeting?.summary || formatMessage(messages.untitled);
    const link = meeting ? {channel_id: meeting.channel_id, kind: 'call' as const, channel_name: channel?.name, display_name: channel?.display_name, channel_type: channel?.type} : undefined;
    const call = useMeeting(link, title);

    if (!meetingId) {
        return <div className={own('error')}>{formatMessage(messages.scheduleCallMissing)}</div>;
    }
    if (error) {
        return <div className={own('error')}>{error}</div>;
    }
    if (!meeting) {
        return null;
    }

    const isOrganizer = meeting.organizer_id === currentUserId;
    const recurring = Boolean(meeting.recurrence_rule);
    const myRSVP = meeting.my_rsvp ?? meeting.rsvps?.[currentUserId]?.status;
    const rsvpLabel: Record<RSVPStatus, string> = {
        going: formatMessage(messages.rsvpGoing),
        maybe: formatMessage(messages.rsvpMaybe),
        no: formatMessage(messages.rsvpNo),
    };

    // The time shown is the call's next (or live) occurrence; once it's over, its first one.
    const start = meeting.next_start ?? meeting.start;
    const end = meeting.next_end ?? meeting.end;
    const when = formatMessage(messages.scheduleCallWhen, {
        start: new Date(start).toLocaleDateString(locale, {weekday: 'short', day: 'numeric', month: 'short'}),
        startTime: formatTime(start, locale),
        endTime: formatTime(end, locale),
    });
    const repeatWords = recurring ? describeRRule(intl, meeting.recurrence_rule || '', new Date(meeting.start)) : '';

    const run = async (action: () => Promise<Meeting>) => {
        setBusy(true);
        try {
            setMeeting(await action());
        } catch (err) {
            setError((err as Error).message);
        } finally {
            setBusy(false);
        }
    };

    const rsvp = (status: RSVPStatus) => run(() => client.setRSVP(meeting.id, status, recurring && nextOnly));

    const callReminder = meeting.reminder_minutes ?? DEFAULT_REMINDER;
    const remindValue = typeof meeting.my_reminder_override === 'number' ? String(meeting.my_reminder_override) : REMIND_DEFAULT;
    const setReminder = (value: string) => run(() => client.setReminderOverride(meeting.id, value === REMIND_DEFAULT ? null : Number(value)));

    const cancel = async (scope: CancelScope) => {
        setChoosingCancel(false);
        const occurrence = scope === 'occurrence';
        let question = messages.cancelCallConfirm;
        if (occurrence) {
            question = messages.cancelOccurrenceConfirm;
        } else if (recurring) {
            question = messages.cancelSeriesConfirm;
        }
        const yes = await confirmDialog({
            title: formatMessage(question),
            message: formatMessage(occurrence ? messages.cancelOccurrenceLead : messages.cancelCallLead),
            confirmLabel: formatMessage(messages.cancelCallConfirmButton),
            cancelLabel: formatMessage(messages.cancel),
            danger: true,
        });
        if (!yes) {
            return;
        }
        await run(() => client.cancelMeeting(meeting.id, scope));
    };

    const canEdit = isOrganizer && !meeting.cancelled;
    const canCancel = Boolean(meeting.can_cancel) && !meeting.cancelled;

    return (
        <div className={own('meeting', {cancelled: meeting.cancelled})}>
            <div className={own('meeting-head')}>
                <span className={own('meeting-title')}>{title}</span>
                {meeting.cancelled && <span className={own('meeting-cancelled-note')}>{formatMessage(messages.callCancelledBadge)}</span>}
            </div>
            {repeatWords && <div className={own('meeting-when')}>{repeatWords}</div>}
            <div className={own('meeting-when')}>
                {recurring && !meeting.cancelled ? formatMessage(messages.nextOccurrence, {when}) : when}
            </div>
            {meeting.description && <div className={own('meeting-desc')}>{meeting.description}</div>}
            {!meeting.cancelled && (
                <div className={own('meeting-actions')}>
                    {call?.button}
                    <a
                        className={am('btn')}
                        href={client.meetingICSURL(meeting.id)}
                        download={true}
                    >
                        {formatMessage(messages.addToCalendar)}
                    </a>
                    <div
                        className={own('rsvp-group')}
                        role='group'
                        aria-label={formatMessage(messages.rsvpQuestion)}
                    >
                        {RSVP_CHOICES.map((status) => (
                            <button
                                key={status}
                                type='button'
                                className={am('chip-btn', {on: myRSVP === status})}
                                aria-pressed={myRSVP === status}
                                disabled={busy}
                                onClick={() => rsvp(status)}
                            >
                                {rsvpLabel[status]}
                            </button>
                        ))}
                    </div>
                    {recurring && (
                        <label className={own('check')}>
                            <input
                                type='checkbox'
                                checked={nextOnly}
                                onChange={(e) => setNextOnly(e.target.checked)}
                            />
                            <span>{formatMessage(messages.rsvpNextOnly)}</span>
                        </label>
                    )}
                </div>
            )}
            {!meeting.cancelled && myRSVP !== 'no' && (
                <label className={own('remind-me')}>
                    <span>{formatMessage(messages.remindMe)}</span>
                    <select
                        className={own('input')}
                        value={remindValue}
                        disabled={busy}
                        onChange={(e) => setReminder(e.target.value)}
                    >
                        <option value={REMIND_DEFAULT}>{formatMessage(messages.remindDefault, {choice: reminderLabel(intl, callReminder)})}</option>
                        {REMINDER_CHOICES.map((minutes) => (
                            <option
                                key={minutes}
                                value={String(minutes)}
                            >
                                {reminderLabel(intl, minutes)}
                            </option>
                        ))}
                    </select>
                </label>
            )}
            {(canEdit || canCancel) && (
                <div className={own('meeting-actions')}>
                    {canEdit && (
                        <button
                            type='button'
                            className={am('btn')}
                            onClick={() => openScheduleCallDialog({channelId: meeting.channel_id, meeting})}
                        >
                            {formatMessage(messages.editCall)}
                        </button>
                    )}
                    {canCancel && (
                        <button
                            type='button'
                            className={am('btn', 'danger')}
                            aria-expanded={recurring ? choosingCancel : undefined}
                            onClick={() => (recurring ? setChoosingCancel(!choosingCancel) : cancel('series'))}
                        >
                            {formatMessage(messages.cancelCall)}
                        </button>
                    )}
                    {canCancel && recurring && choosingCancel && (
                        <div
                            className={own('rsvp-group')}
                            role='group'
                            aria-label={formatMessage(messages.cancelWhich)}
                        >
                            <button
                                type='button'
                                className={am('chip-btn')}
                                disabled={busy || !meeting.next_start}
                                onClick={() => cancel('occurrence')}
                            >
                                {formatMessage(messages.cancelThisCall)}
                            </button>
                            <button
                                type='button'
                                className={am('chip-btn')}
                                disabled={busy}
                                onClick={() => cancel('series')}
                            >
                                {formatMessage(messages.cancelWholeSeries)}
                            </button>
                        </div>
                    )}
                </div>
            )}
        </div>
    );
}
