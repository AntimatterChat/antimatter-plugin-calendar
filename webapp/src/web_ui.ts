// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

// Antimatter ships two web UIs: the classic one and Fusion. Under Fusion, the calendar panel is drawn
// with Fusion's own styles, as in its mockup. Both UIs say which one is running before plugins
// load.

export type WebUI = 'classic' | 'fusion';

// isFusionUI returns whether the Fusion web UI is running.
export function isFusionUI(): boolean {
    return window.antimatterWebUI === 'fusion' || document.documentElement.dataset.amWebUi === 'fusion';
}
