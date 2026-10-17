// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {iconTip, tip} from './tip';

const setWebUI = (ui?: 'classic' | 'fusion') => {
    window.antimatterWebUI = ui;
    if (ui) {
        document.documentElement.dataset.amWebUi = ui;
    } else {
        delete document.documentElement.dataset.amWebUi;
    }
};

afterEach(() => setWebUI(undefined));

describe('tip', () => {
    test('under Fusion, is Fusion\'s tooltip (data-am-tip), with no browser tooltip', () => {
        setWebUI('fusion');
        expect(tip('Delete')).toEqual({'data-am-tip': 'Delete'});
        expect(iconTip('Delete')).toEqual({'data-am-tip': 'Delete', 'aria-label': 'Delete'});
    });

    test('under the classic UI, keeps the browser\'s tooltip (title)', () => {
        setWebUI('classic');
        expect(tip('Delete')).toEqual({title: 'Delete'});
        expect(iconTip('Delete')).toEqual({title: 'Delete', 'aria-label': 'Delete'});
    });

    test('Fusion is also known from the page alone (data-am-web-ui)', () => {
        document.documentElement.dataset.amWebUi = 'fusion';
        expect(tip('Back')).toEqual({'data-am-tip': 'Back'});
    });
});
