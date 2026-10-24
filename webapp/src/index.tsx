// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';

import {Client4} from 'mattermost-redux/client';

import CalendarPanel from './components/calendar_panel';
import {messages} from './components/messages';
import MeetingPost from './components/meeting_post';
import ScheduleCallDialogRoot from './components/schedule_call_dialog';
import manifest from './manifest';
import {meetingChanged} from './meeting_events';
import {openScheduleCallDialog} from './schedule_call';
import type {PluginClass, PluginRegistry, PluginStore} from './types/host';
import {registerTranslations} from './translations';

import './styles.css';

// meetingPostType is the server's meetingPostType (meetings_api.go): a call scheduled for the
// channel.
const MEETING_POST_TYPE = 'custom_calendar_meeting';

const ICON_COLOR = '#FB7185';

// calendarIconURL returns the app bar icon. The app bar only takes image URLs.
function calendarIconURL(): string {
    const svg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">' +
        `<g fill="none" stroke="${ICON_COLOR}" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">` +
        '<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M3 10h18M8 3v4M16 3v4"/><path d="M8 14h3v3H8z"/></g></svg>';
    return 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg);
}

export default class Plugin implements PluginClass {
    public initialize(registry: PluginRegistry, store: PluginStore) {
        registerTranslations(registry);

        if (window.basename) {
            Client4.setUrl(window.basename);
        }

        const title = (
            <FormattedMessage
                id='calendar.title'
                defaultMessage='Calendar'
            />
        );
        const rhs = registry.registerRightHandSidebarComponent(CalendarPanel, title);
        registry.registerAppBarComponent({
            iconUrl: calendarIconURL(),
            tooltipText: title,
            action: () => store.dispatch(rhs.toggleRHSPlugin),
        });

        // Scheduling a call for everyone in the channel: the "..." menu opens the dialog (there's
        // no call button of this app's own to add it to); the dialog and the meeting's post are
        // both registered once, and read what to show from the store and the server themselves.
        registry.registerChannelHeaderMenuAction(
            <FormattedMessage {...messages.scheduleCallMenuItem}/>,
            (channelId) => openScheduleCallDialog({channelId}),
        );
        registry.registerRootComponent(ScheduleCallDialogRoot);
        registry.registerPostTypeComponent(MEETING_POST_TYPE, MeetingPost);
        registry.registerWebSocketEventHandler(`custom_${manifest.id}_meeting_changed`, (msg) => {
            if (typeof msg.data.id === 'string') {
                meetingChanged(msg.data.id);
            }
        });
    }
}

window.registerPlugin(manifest.id, new Plugin());
