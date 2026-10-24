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
import type {Meeting, RSVPStatus} from '../client';
import {onMeetingChanged} from '../meeting_events';
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

// MeetingPost is the post of a call scheduled for the channel (registerPostTypeComponent
// 'custom_calendar_meeting'): its time, a Join button when Calls or Voice channels can open it,
// "Add to calendar", RSVP, and, for its organiser, Edit and Cancel.
export default function MeetingPost({post}: Props) {
    const {formatMessage, locale} = useIntl();
    const meetingId = meetingIdOf(post);
    const currentUserId = useSelector(getCurrentUserId);
    const channel = useSelector((state: GlobalState) => getChannel(state, post.channel_id));

    const [meeting, setMeeting] = useState<Meeting | null>(null);
    const [error, setError] = useState('');
    const [rsvpBusy, setRSVPBusy] = useState(false);

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
    const myRSVP = meeting.rsvps?.[currentUserId]?.status;
    const rsvpLabel: Record<RSVPStatus, string> = {
        going: formatMessage(messages.rsvpGoing),
        maybe: formatMessage(messages.rsvpMaybe),
        no: formatMessage(messages.rsvpNo),
    };

    const rsvp = async (status: RSVPStatus) => {
        setRSVPBusy(true);
        try {
            setMeeting(await client.setRSVP(meeting.id, status));
        } catch (err) {
            setError((err as Error).message);
        } finally {
            setRSVPBusy(false);
        }
    };

    const cancelMeeting = async () => {
        const yes = await confirmDialog({
            title: formatMessage(messages.cancelCallConfirm),
            message: formatMessage(messages.cancelCallLead),
            confirmLabel: formatMessage(messages.cancelCallConfirmButton),
            cancelLabel: formatMessage(messages.cancel),
            danger: true,
        });
        if (!yes) {
            return;
        }
        try {
            await client.cancelMeeting(meeting.id);
        } catch (err) {
            setError((err as Error).message);
        }
    };

    return (
        <div className={own('meeting', {cancelled: meeting.cancelled})}>
            <div className={own('meeting-head')}>
                <span className={own('meeting-title')}>{title}</span>
                {meeting.cancelled && <span className={own('meeting-cancelled-note')}>{formatMessage(messages.callCancelledBadge)}</span>}
            </div>
            <div className={own('meeting-when')}>
                {formatMessage(messages.scheduleCallWhen, {
                    start: new Date(meeting.start).toLocaleDateString(locale, {weekday: 'short', day: 'numeric', month: 'short'}),
                    startTime: formatTime(meeting.start, locale),
                    endTime: formatTime(meeting.end, locale),
                })}
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
                                disabled={rsvpBusy}
                                onClick={() => rsvp(status)}
                            >
                                {rsvpLabel[status]}
                            </button>
                        ))}
                    </div>
                </div>
            )}
            {isOrganizer && (
                <div className={own('meeting-actions')}>
                    {!meeting.cancelled && (
                        <button
                            type='button'
                            className={am('btn')}
                            onClick={() => openScheduleCallDialog({channelId: meeting.channel_id, meeting})}
                        >
                            {formatMessage(messages.editCall)}
                        </button>
                    )}
                    {!meeting.cancelled && (
                        <button
                            type='button'
                            className={am('btn', 'danger')}
                            onClick={cancelMeeting}
                        >
                            {formatMessage(messages.cancelCall)}
                        </button>
                    )}
                </div>
            )}
        </div>
    );
}
