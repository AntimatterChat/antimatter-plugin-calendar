// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React, {useEffect, useReducer} from 'react';
import {useIntl} from 'react-intl';
import {useSelector} from 'react-redux';

import type {GlobalState} from '@mattermost/types/store';

import {getCurrentTeam} from 'mattermost-redux/selectors/entities/teams';

import {am, own} from '../class_names';
import type {EventLink} from '../client';

import Icon from './icon';
import {messages} from './messages';

// useMeetingAPIs re-renders when the Calls or Voice channels plugin installs its API, which may
// happen after this plugin loaded.
function useMeetingAPIs() {
    const [, update] = useReducer((n: number) => n + 1, 0);
    useEffect(() => {
        window.addEventListener('antimatter-calls:ready', update);
        window.addEventListener('antimatter-voice-channels:ready', update);
        return () => {
            window.removeEventListener('antimatter-calls:ready', update);
            window.removeEventListener('antimatter-voice-channels:ready', update);
        };
    }, []);
    return {calls: window.antimatterCalls, voice: window.antimatterVoiceChannels};
}

export type Meeting = {

    // What the event row says about the meeting place
    place: React.ReactNode;

    // The button that joins it
    button: React.ReactNode;
};

function openChannel(link: EventLink, teamName: string) {
    const team = link.team_name || teamName;
    if (team && link.channel_name) {
        window.WebappUtils?.browserHistory.push(`/${team}/channels/${link.channel_name}`);
    }
}

// useMeeting returns how to join the channel, voice channel or call an event is linked to: with
// the Voice channels plugin for voice channels, the Calls plugin for calls, or by opening the
// channel otherwise.
export function useMeeting(link: EventLink | undefined, title: string): Meeting | null {
    const {formatMessage} = useIntl();
    const {calls, voice} = useMeetingAPIs();
    const channelId = link?.channel_id || '';
    const isVoice = useSelector((state: GlobalState) => Boolean(channelId && voice?.selectors.isVoiceChannel(state, channelId)));
    const live = useSelector((state: GlobalState) => Boolean(channelId && calls?.selectors.getCall(state, channelId)));
    const currentTeam = useSelector(getCurrentTeam);

    if (!link || !link.display_name) {
        // Not linked, or to a channel the user can't read
        return null;
    }
    const name = link.display_name;
    const direct = link.channel_type === 'D' || link.channel_type === 'G';

    if (link.kind === 'call' && calls) {
        return {
            place: (
                <>
                    <Icon
                        name='phone'
                        size='xs'
                    />
                    {' '}
                    {formatMessage(direct ? messages.callWith : messages.callIn, {channel: `#${name}`, name})}
                </>
            ),
            button: (
                <button
                    className={am('btn') + ' ' + own('join', 'join-call')}
                    onClick={() => calls.join(channelId, {title})}
                >
                    {formatMessage(live ? messages.joinCall : messages.startCall)}
                </button>
            ),
        };
    }

    if (isVoice && voice) {
        return {
            place: (
                <>
                    <Icon
                        name='speaker'
                        size='xs'
                    />
                    {' '}
                    {name}
                </>
            ),
            button: (
                <button
                    className={am('btn', 'anti') + ' ' + own('join')}
                    onClick={() => {
                        openChannel(link, currentTeam?.name || '');
                        voice.join(channelId);
                    }}
                >
                    {formatMessage(messages.join)}
                </button>
            ),
        };
    }

    return {
        place: (
            <>
                <Icon
                    name='hash'
                    size='xs'
                />
                {' '}
                {name}
            </>
        ),
        button: (
            <button
                className={am('btn') + ' ' + own('join')}
                onClick={() => openChannel(link, currentTeam?.name || '')}
            >
                {formatMessage(messages.open)}
            </button>
        ),
    };
}
