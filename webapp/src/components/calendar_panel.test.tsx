// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {render, screen} from '@testing-library/react';
import React from 'react';
import {IntlProvider} from 'react-intl';

import * as client from '../client';

import CalendarPanel, {APP_COLOR} from './calendar_panel';

jest.mock('react-redux', () => ({useSelector: (selector: (state: object) => unknown) => selector({})}));
jest.mock('mattermost-redux/selectors/entities/teams', () => ({getCurrentTeam: () => null}));
jest.mock('../client', () => ({
    ...jest.requireActual('../client'),
    getAccount: jest.fn(),
    getConfig: jest.fn(),
    getCalendars: jest.fn(),
    getEvents: jest.fn(),
}));

const mocked = client as jest.Mocked<typeof client>;

const setWebUI = (ui?: 'classic' | 'fusion') => {
    window.antimatterWebUI = ui;
    if (ui) {
        document.documentElement.dataset.amWebUi = ui;
    } else {
        delete document.documentElement.dataset.amWebUi;
    }
};

const renderPanel = () => render(
    <IntlProvider locale='en'>
        <CalendarPanel/>
    </IntlProvider>,
);

// The panel's toolbar buttons that show only an icon.
const ICON_BUTTONS = ['New event', 'Refresh', 'Account settings', 'Previous', 'Next'];

describe('CalendarPanel', () => {
    beforeEach(() => {
        mocked.getAccount.mockResolvedValue({url: 'https://dav.example.com/', username: 'maya', remind_minutes: 10});
        mocked.getConfig.mockResolvedValue({reminders_enabled: true, allow_insecure_connections: false});
        mocked.getCalendars.mockResolvedValue([{id: '/cal/work/', name: 'Work', read_only: false}]);
        mocked.getEvents.mockResolvedValue({events: [], errors: []});
    });

    afterEach(() => setWebUI(undefined));

    describe('under Fusion', () => {
        beforeEach(() => setWebUI('fusion'));

        test('loads with a quiet busy line, then shows the empty day as Fusion\'s empty state', async () => {
            mocked.getEvents.mockReturnValue(new Promise(() => {}));
            const {unmount} = renderPanel();
            const loading = screen.getByRole('status');
            expect(loading).toHaveClass('am-empty', 'amfx-empty', 'amfx-busy');
            expect(loading).toHaveTextContent('Loading…');
            unmount();

            mocked.getEvents.mockResolvedValue({events: [], errors: []});
            renderPanel();
            const empty = await screen.findByTestId('calendar-day-empty');
            expect(empty).toHaveClass('am-empty', 'amfx-empty');
            expect(empty).toHaveTextContent('Nothing planned.');
            expect(empty.querySelector('.amfx-empty-ic use')).toHaveAttribute('href', '#am-i-calendar');
        });

        test('gives the panel Calendar\'s colour for Fusion\'s parts that take the app\'s colour', async () => {
            const {container} = renderPanel();
            await screen.findByTestId('calendar-day-empty');
            const root = container.firstElementChild as HTMLElement;
            expect(root.style.getPropertyValue('--am-cc')).toBe(APP_COLOR);
        });

        test.each(ICON_BUTTONS)('the %s button has Fusion\'s tooltip and keeps its name', async (label) => {
            renderPanel();
            await screen.findByTestId('calendar-day-empty');
            const button = screen.getByRole('button', {name: label});
            expect(button).toHaveAttribute('data-am-tip', label);
            expect(button).not.toHaveAttribute('title');
        });

        test('draws Fusion\'s sprite icons, turned for the arrows, and its own refresh arrow', async () => {
            renderPanel();
            await screen.findByTestId('calendar-day-empty');
            expect(screen.getByRole('button', {name: 'New event'}).querySelector('use')).toHaveAttribute('href', '#am-i-plus');
            expect(screen.getByRole('button', {name: 'Account settings'}).querySelector('use')).toHaveAttribute('href', '#am-i-cog');
            const previous = screen.getByRole('button', {name: 'Previous'}).querySelector('svg')!;
            expect(previous.querySelector('use')).toHaveAttribute('href', '#am-i-chev');
            expect(previous.style.transform).toBe('rotate(90deg)');
            expect((screen.getByRole('button', {name: 'Next'}).querySelector('svg') as SVGElement).style.transform).toBe('rotate(-90deg)');

            // No sprite symbol for it: the panel's own drawing.
            const refresh = screen.getByRole('button', {name: 'Refresh'}).querySelector('svg')!;
            expect(refresh.querySelector('use')).toBeNull();
            expect(refresh.querySelector('path')).not.toBeNull();
        });
    });

    describe('under the classic UI', () => {
        beforeEach(() => setWebUI('classic'));

        test('keeps the browser\'s tooltips, its own icons and its empty state', async () => {
            renderPanel();
            const empty = await screen.findByTestId('calendar-day-empty');
            expect(empty.className).toBe('amc-empty');
            expect(empty.querySelector('svg')).toBeNull();
            for (const label of ICON_BUTTONS) {
                const button = screen.getByRole('button', {name: label});
                expect(button).toHaveAttribute('title', label);
                expect(button).not.toHaveAttribute('data-am-tip');
                expect(button.querySelector('use')).toBeNull();
            }
        });
    });
});
