// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';

import {Client4} from 'mattermost-redux/client';

import CalendarPanel from './components/calendar_panel';
import manifest from './manifest';
import type {PluginClass, PluginRegistry, PluginStore} from './types/host';

import './styles.css';

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
    }
}

window.registerPlugin(manifest.id, new Plugin());
