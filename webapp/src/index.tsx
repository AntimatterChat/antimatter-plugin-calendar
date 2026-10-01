// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import manifest from './manifest';
import type {PluginClass} from './types/host';

export default class Plugin implements PluginClass {
    public initialize() {
        // The plugin registers its components here.
    }
}

window.registerPlugin(manifest.id, new Plugin());
