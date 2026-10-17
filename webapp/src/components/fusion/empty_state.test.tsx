// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {fireEvent, render, screen} from '@testing-library/react';
import React from 'react';

import EmptyState from './empty_state';

const setWebUI = (ui?: 'classic' | 'fusion') => {
    window.antimatterWebUI = ui;
    if (ui) {
        document.documentElement.dataset.amWebUi = ui;
    } else {
        delete document.documentElement.dataset.amWebUi;
    }
};

afterEach(() => setWebUI(undefined));

const icon = (
    <svg
        className='am-ic'
        data-testid='icon'
    />
);

describe('EmptyState', () => {
    test('under Fusion, is Fusion\'s empty state: centred, an icon, a title, a sentence and one primary action', () => {
        setWebUI('fusion');
        const onClick = jest.fn();
        render(
            <EmptyState
                data-testid='empty'
                icon={icon}
                title='No notes yet'
                text='Notes are shared with everyone here.'
                action={{label: 'New note', onClick}}
                classicClassName='old-empty'
            />,
        );
        const root = screen.getByTestId('empty');
        expect(root.className).toBe('am-empty amfx-empty');
        expect(root.getAttribute('data-empty-state')).toBe('true');
        expect(root.querySelector('.amfx-empty-ic [data-testid="icon"]')).not.toBeNull();
        expect(root.querySelector('.amfx-empty-title')!.textContent).toBe('No notes yet');
        expect(root.querySelector('.amfx-empty-text')!.textContent).toBe('Notes are shared with everyone here.');
        const buttons = root.querySelectorAll('button');
        expect(buttons).toHaveLength(1);
        expect(buttons[0].className).toBe('am-btn am-primary');
        fireEvent.click(buttons[0]);
        expect(onClick).toHaveBeenCalledTimes(1);
    });

    test('while loading, is one quiet line announced as busy, not an ellipsis', () => {
        setWebUI('fusion');
        render(
            <EmptyState
                data-testid='empty'
                busy={true}
                icon={icon}
                text='Loading notes…'
            />,
        );
        const root = screen.getByRole('status');
        expect(root).toBe(screen.getByTestId('empty'));
        expect(root.className).toBe('am-empty amfx-empty amfx-busy');
        expect(root.getAttribute('aria-busy')).toBe('true');
        expect(root.textContent).toBe('Loading notes…');
        expect(screen.queryByTestId('icon')).toBeNull();
    });

    test('an error takes the danger tone', () => {
        setWebUI('fusion');
        render(
            <EmptyState
                data-testid='empty'
                tone='danger'
                title='The note couldn’t be loaded'
            />,
        );
        expect(screen.getByTestId('empty').className).toBe('am-empty amfx-empty amfx-danger');
    });

    test('under the classic UI, keeps the app\'s own classes and draws no Fusion icon', () => {
        setWebUI('classic');
        render(
            <EmptyState
                data-testid='empty'
                icon={icon}
                title='No notes yet'
                action={{label: 'New note', onClick: jest.fn()}}
                classicClassName='old-empty'
            />,
        );
        const root = screen.getByTestId('empty');
        expect(root.className).toBe('old-empty');
        expect(root.querySelector('[class*="am-"]')).toBeNull();
        expect(screen.queryByTestId('icon')).toBeNull();
        expect(root.textContent).toContain('No notes yet');
        expect(root.querySelector('button')!.className).toBe('btn btn-primary');
    });
});
