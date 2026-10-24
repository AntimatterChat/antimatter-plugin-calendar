// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import type React from 'react';
import type {AnyAction, Store} from 'redux';

import type {Channel} from '@mattermost/types/channels';
import type {Post} from '@mattermost/types/posts';
import type {GlobalState} from '@mattermost/types/store';

import type {WebUI} from '../web_ui';

export type RightHandSidebarRegistration = {
    id: string;
    showRHSPlugin: AnyAction;
    hideRHSPlugin: AnyAction;
    toggleRHSPlugin: AnyAction;
};

// The subset of the host's plugin registry this plugin uses.
export interface PluginRegistry {
    registerRightHandSidebarComponent(component: React.ComponentType, title: React.ReactNode): RightHandSidebarRegistration;
    registerAppBarComponent(options: {iconUrl: string; tooltipText: React.ReactNode; action: () => void}): string;

    // The channel's "..." menu: where "Schedule a call" lives (there's no channel call button of
    // the calendar's own to add it to; see components/schedule_call.tsx).
    registerChannelHeaderMenuAction(text: React.ReactNode, action: (channelId: string) => void, shouldRender?: (state: GlobalState) => boolean): string;

    // The channel header's next call: Fusion's place for apps' chips beside the conversation's
    // name (absent from older Fusions and from the classic web app), and the classic header's
    // place for apps beside the channel's name. Both pass the channel.
    registerChannelHeaderChip?(component: React.ComponentType<{channel?: Channel}>): string;
    registerChannelHeaderIcon?(component: React.ComponentType<{channel?: Channel}>): string;

    // The scheduling dialog, and the meeting post's buttons, are both rendered this way.
    registerRootComponent(component: React.ComponentType): string;
    registerPostTypeComponent(type: string, component: React.ComponentType<{post: Post}>): string;
    registerWebSocketEventHandler(event: string, handler: (msg: {event: string; data: Record<string, unknown>}) => void): void;
}

export type PluginStore = Store<GlobalState>;

export interface PluginClass {
    initialize(registry: PluginRegistry, store: PluginStore): void | Promise<void>;
    uninitialize?(): void;
}

// The parts of the Calls plugin's API (window.antimatterCalls) the calendar uses to join the
// calls events are linked to.
export interface AntimatterCallsAPI {
    version: number;
    selectors: {
        isCallsEnabled(state: GlobalState, channelId: string): boolean;
        getCall(state: GlobalState, channelId: string): {id: string; startAt: number} | null;
    };
    join(channelId: string, opts?: {title?: string; switchCall?: boolean}): Promise<void>;
}

// The parts of the Voice channels plugin's API (window.antimatterVoiceChannels) the calendar
// uses to join the voice channels events are linked to.
export interface AntimatterVoiceChannelsAPI {
    version: number;
    selectors: {
        isVoiceChannel(state: GlobalState, channelId: string): boolean;
    };
    join(channelId: string, opts?: {leaveOtherCalls?: boolean}): Promise<void>;
}

declare global {
    interface Window {
        registerPlugin(pluginId: string, plugin: PluginClass): void;
        basename?: string;

        // Set by the Antimatter web UIs before plugins load
        antimatterWebUI?: WebUI;

        // Set by the Calls and Voice channels plugins when they're installed
        antimatterCalls?: AntimatterCallsAPI;
        antimatterVoiceChannels?: AntimatterVoiceChannelsAPI;

        WebappUtils?: {
            browserHistory: {push: (path: string) => void};
        };
    }
}
