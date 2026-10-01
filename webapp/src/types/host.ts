// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import type {Store} from 'redux';

import type {GlobalState} from '@mattermost/types/store';

// The subset of the host's plugin registry this plugin uses. Hooks that older hosts may lack
// are optional: the plugin checks for them before use.
export interface PluginRegistry {
    registerReducer(reducer: unknown): void;
}

export type PluginStore = Store<GlobalState>;

export interface PluginClass {
    initialize(registry: PluginRegistry, store: PluginStore): void | Promise<void>;
    uninitialize?(): void;
}

declare global {
    interface Window {
        registerPlugin(pluginId: string, plugin: PluginClass): void;
        basename?: string;
    }
}
