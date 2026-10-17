// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

// The Fusion helpers shared by Antimatter's default apps: the same files (src/components/fusion/) in each app, with
// the same API. Antimatter ships two web UIs, Fusion (the default) and the classic one, and both say which one runs
// before apps load (window.antimatterWebUI, and data-am-web-ui on <html>). Under Fusion the helpers draw Fusion's
// markup (its am- classes, its icon sprite, its layer); under the classic UI they keep the classic behaviour.
// (window.antimatterWebUI is declared with the host's other globals, in src/types/host.ts.)

// The Fusion UI's layer for popovers, dialogs and tooltips (.am-layer), whose styles apply to what it contains.
export const FUSION_LAYER_ID = 'am-layer';

// isFusion returns whether the Fusion web UI is running.
export function isFusion(): boolean {
    return window.antimatterWebUI === 'fusion' || document.documentElement.dataset.amWebUi === 'fusion';
}

// fusionLayer returns Fusion's layer, or null outside the Fusion UI (or in a page without it).
export function fusionLayer(): HTMLElement | null {
    return isFusion() ? document.getElementById(FUSION_LAYER_ID) : null;
}
