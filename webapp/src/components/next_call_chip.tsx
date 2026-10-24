// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import {useSelector} from 'react-redux';

import type {Channel} from '@mattermost/types/channels';

import {getCurrentTeam} from 'mattermost-redux/selectors/entities/teams';

import {am, own} from '../class_names';
import * as client from '../client';
import type {NextCall} from '../client';
import {openPost} from '../community_calls';
import {onMeetingChanged} from '../meeting_events';
import {MIN_FETCH_GAP_MS, REFRESH_MS, chipState, nextFlip} from '../next_call';
import {formatTime} from '../utils';

import {messages} from './messages';

type Props = {

    // The conversation whose header it is (Fusion's registerChannelHeaderChip, or the classic
    // header's ChannelHeaderIcon place).
    channel?: Channel;
};

// NextCallChip is the channel's next scheduled call in its header: "Next call: Tue 14:00 · Title",
// "Next call: in N min · Title" in the last hour, "Live · Title" (and Join) while it's on; it opens
// the call's post. Nothing in direct and group messages, or when there's no call within 7 days.
export default function NextCallChip({channel}: Props) {
    const {formatMessage, locale} = useIntl();
    const team = useSelector(getCurrentTeam);
    const channelId = channel?.id || '';
    const eligible = Boolean(channelId) && channel?.type !== 'D' && channel?.type !== 'G';

    const [next, setNext] = useState<NextCall | null>(null);
    const [now, setNow] = useState(() => Date.now());
    const lastFetch = useRef(0);

    // The channel shown now: a response for the one before a switch is dropped.
    const current = useRef(channelId);
    current.current = channelId;

    const load = useCallback(() => {
        if (!eligible) {
            setNext(null);
            return;
        }
        lastFetch.current = Date.now();
        client.getNextCall(channelId).then((call) => {
            if (current.current === channelId) {
                setNext(call);
                setNow(Date.now());
            }
        }).catch(() => {
            if (current.current === channelId) {
                setNext(null);
            }
        });
    }, [channelId, eligible]);

    // On a channel switch, and whenever the server says one of this channel's calls changed.
    useEffect(() => {
        setNext(null);
        load();
    }, [load]);
    useEffect(() => onMeetingChanged((meetingId, changedChannelId) => {
        if (!changedChannelId || changedChannelId === channelId) {
            load();
        }
    }), [load, channelId]);
    useEffect(() => {
        const interval = eligible ? window.setInterval(load, REFRESH_MS) : 0;
        return () => window.clearInterval(interval);
    }, [load, eligible]);

    // The chip's own clock: it wakes up only when what it says changes, and once the call is
    // over reads the next one (no sooner than a minute after the last read).
    useEffect(() => {
        const delay = nextFlip(next, now);
        let timer = 0;
        if (delay !== null) {
            timer = window.setTimeout(() => setNow(Date.now()), delay);
        } else if (next) {
            timer = window.setTimeout(load, Math.max(0, (lastFetch.current + MIN_FETCH_GAP_MS) - Date.now()));
        }
        return () => window.clearTimeout(timer);
    }, [next, now, load]);

    const state = chipState(next, now);
    if (!eligible || !next || state.kind === 'hidden') {
        return null;
    }

    const title = next.summary || formatMessage(messages.untitled);
    const live = state.kind === 'live';
    let text: string;
    if (live) {
        text = formatMessage(messages.nextCallLive, {title});
    } else if (state.kind === 'soon') {
        text = formatMessage(messages.nextCallSoon, {minutes: state.minutes, title});
    } else {
        const when = `${new Date(next.start).toLocaleDateString(locale, {weekday: 'short'})} ${formatTime(next.start, locale)}`;
        text = formatMessage(messages.nextCall, {when, title});
    }
    const calls = window.antimatterCalls;

    return (
        <span className={own('next-call')}>
            <button
                type='button'
                className={am('head-chip', {live})}
                title={formatMessage(messages.nextCallOpen)}
                onClick={() => openPost(team?.name, next.post_id)}
            >
                {live && <i className={own('live-dot')}/>}
                <span>{text}</span>
            </button>
            {live && calls && (
                <button
                    type='button'
                    className={am('head-chip', 'live')}
                    onClick={() => calls.join(channelId, {title})}
                >
                    {formatMessage(messages.join)}
                </button>
            )}
        </span>
    );
}
