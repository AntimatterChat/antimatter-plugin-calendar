// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {act, fireEvent, render, screen, waitFor, within} from '@testing-library/react';
import React from 'react';
import {IntlProvider} from 'react-intl';

import * as client from '../client';
import type {CommunityCall} from '../client';
import {meetingChanged} from '../meeting_events';

import CalendarPanel, {APP_COLOR} from './calendar_panel';

let mockTeam: {id: string; name: string} | null = null;
jest.mock('react-redux', () => ({useSelector: (selector: (state: object) => unknown) => selector({})}));
jest.mock('mattermost-redux/selectors/entities/teams', () => ({getCurrentTeam: () => mockTeam}));
jest.mock('mattermost-redux/selectors/entities/channels', () => ({
    getMyChannels: () => [],
    getCurrentChannelId: () => '',
}));
jest.mock('../client', () => ({
    ...jest.requireActual('../client'),
    getAccount: jest.fn(),
    getConfig: jest.fn(),
    getCalendars: jest.fn(),
    getEvents: jest.fn(),
    getCommunityCalls: jest.fn(),
    getFeed: jest.fn(),
    createFeed: jest.fn(),
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
        mocked.getAccount.mockResolvedValue({url: 'https://dav.example.com/', username: 'maya', remind_minutes: 10, email_invites: false});
        mocked.getConfig.mockResolvedValue({reminders_enabled: true, allow_insecure_connections: false});
        mocked.getCalendars.mockResolvedValue([{id: '/cal/work/', name: 'Work', read_only: false}]);
        mocked.getEvents.mockResolvedValue({events: [], errors: []});
    });

    afterEach(() => {
        setWebUI(undefined);
        mockTeam = null;
        window.localStorage.clear();
        jest.clearAllMocks();
    });

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

    describe('the community\'s calls', () => {
        const inAnHour = Date.now() + (60 * 60000);
        const call = (overrides: Partial<CommunityCall> = {}): CommunityCall => ({
            meeting_id: 'meeting1',
            post_id: 'post1',
            channel_id: 'channel1',
            channel_name: 'standup',
            channel_display_name: 'Standup',
            channel_type: 'O',
            organizer_id: 'organizer1',
            summary: 'Standup sync',
            start: inAnHour,
            end: inAnHour + (30 * 60000),
            recurring: false,
            my_rsvp: 'going',
            ...overrides,
        });

        beforeEach(() => {
            setWebUI('fusion');
            mockTeam = {id: 'team1', name: 'lumen'};
            mocked.getCommunityCalls.mockResolvedValue({occurrences: [call()], truncated: false});
        });

        test('show without a calendar account, with their channel and the person\'s own answer', async () => {
            mocked.getAccount.mockResolvedValue(null);
            renderPanel();

            expect(await screen.findByText('Standup sync')).toBeInTheDocument();
            expect(screen.getByText(/Going/)).toBeInTheDocument();
            expect(screen.getByText(/Standup$/)).toBeInTheDocument();
            expect(mocked.getCommunityCalls).toHaveBeenCalledWith('team1', expect.any(Date), expect.any(Date));
            expect(mocked.getEvents).not.toHaveBeenCalled();

            // No setup form in the way; the settings button connects an account.
            expect(screen.queryByText('Connect your calendar')).not.toBeInTheDocument();
            expect(screen.getByRole('button', {name: 'Connect your calendar'})).toBeInTheDocument();
        });

        test('without a community either, the account setup is shown as before', async () => {
            mockTeam = null;
            mocked.getAccount.mockResolvedValue(null);
            renderPanel();

            expect(await screen.findByText('Connect your calendar')).toBeInTheDocument();
            expect(mocked.getCommunityCalls).not.toHaveBeenCalled();
        });

        test('are a layer toggled beside the personal calendars, in their own colour', async () => {
            mocked.getEvents.mockResolvedValue({events: [], errors: []});
            renderPanel();
            await screen.findByText('Standup sync');

            const layer = screen.getByRole('button', {name: 'Community calls'});
            expect(layer).toHaveAttribute('aria-pressed', 'true');
            expect(screen.getByRole('button', {name: 'Work'})).toBeInTheDocument();
            expect((layer.querySelector('i') as HTMLElement).style.background).not.toBe('');

            fireEvent.click(layer);
            expect(screen.queryByText('Standup sync')).not.toBeInTheDocument();
            expect(screen.getByRole('button', {name: 'Community calls'})).toHaveAttribute('aria-pressed', 'false');

            fireEvent.click(screen.getByRole('button', {name: 'Community calls'}));
            expect(screen.getByText('Standup sync')).toBeInTheDocument();
        });

        test('open their post when clicked, rather than the event editor', async () => {
            const push = jest.fn();
            window.WebappUtils = {browserHistory: {push}};
            renderPanel();

            fireEvent.click(await screen.findByRole('button', {name: 'Open the post of Standup sync'}));
            expect(push).toHaveBeenCalledWith('/lumen/pl/post1');
            expect(screen.queryByText('Edit event')).not.toBeInTheDocument();
            delete window.WebappUtils;
        });

        test('offer Join only while the call is on', async () => {
            window.antimatterCalls = {
                version: 1,
                selectors: {isCallsEnabled: () => true, getCall: () => null},
                join: jest.fn(),
            };
            const now = Date.now();
            mocked.getCommunityCalls.mockResolvedValue({
                occurrences: [
                    call(),
                    call({meeting_id: 'meeting2', post_id: 'post2', summary: 'Live review', start: now - (10 * 60000), end: now + (20 * 60000)}),
                ],
                truncated: false,
            });
            renderPanel();
            await screen.findByText('Live review');

            const buttons = screen.getAllByRole('button', {name: 'Start call'});
            expect(buttons).toHaveLength(1);
            expect(buttons[0].closest('.am-event')).toHaveTextContent('Live review');
            delete window.antimatterCalls;
        });

        test('are read again when the server says a call changed', async () => {
            renderPanel();
            await screen.findByText('Standup sync');
            expect(mocked.getCommunityCalls).toHaveBeenCalledTimes(1);

            mocked.getCommunityCalls.mockResolvedValue({occurrences: [call({summary: 'Standup sync (moved)'})], truncated: false});
            act(() => meetingChanged('meeting1'));
            await waitFor(() => expect(mocked.getCommunityCalls).toHaveBeenCalledTimes(2));
            expect(await screen.findByText('Standup sync (moved)')).toBeInTheDocument();
        });

        test('Subscribe… is the one entry of the layer\'s own menu, and opens the private link\'s dialog', async () => {
            mocked.getConfig.mockResolvedValue({reminders_enabled: true, allow_insecure_connections: false, calendar_feeds_enabled: true});
            mocked.getFeed.mockResolvedValue({exists: false});
            mocked.createFeed.mockResolvedValue({url: 'https://chat.example.com/plugins/com.antimatterchat.calendar/feeds/abc.ics', path: '/plugins/com.antimatterchat.calendar/feeds/abc.ics', created_at: 1});
            renderPanel();
            await screen.findByText('Standup sync');

            const menuButton = screen.getByRole('button', {name: 'Community calls options'});
            expect(menuButton).toHaveAttribute('aria-haspopup', 'menu');
            expect(menuButton).toHaveAttribute('aria-expanded', 'false');
            expect(screen.queryByRole('menuitem', {name: 'Subscribe…'})).not.toBeInTheDocument();

            fireEvent.click(menuButton);
            const menu = screen.getByRole('menu', {name: 'Community calls options'});
            expect(within(menu).getAllByRole('menuitem')).toHaveLength(1);
            expect(screen.getAllByText('Subscribe…')).toHaveLength(1);
            expect(mocked.getFeed).not.toHaveBeenCalled();

            fireEvent.click(within(menu).getByRole('menuitem', {name: 'Subscribe…'}));
            expect(screen.queryByRole('menu')).not.toBeInTheDocument();
            expect(screen.getByRole('dialog', {name: 'Subscribe to community calls'})).toBeInTheDocument();
            expect(await screen.findByLabelText('Calendar link')).toHaveValue('https://chat.example.com/plugins/com.antimatterchat.calendar/feeds/abc.ics');
            expect(mocked.getFeed).toHaveBeenCalledWith('team1');

            fireEvent.click(screen.getByRole('button', {name: 'Close'}));
            expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
        });

        test('the layer\'s menu closes on Escape or a click elsewhere', async () => {
            mocked.getConfig.mockResolvedValue({reminders_enabled: true, allow_insecure_connections: false, calendar_feeds_enabled: true});
            renderPanel();
            await screen.findByText('Standup sync');

            fireEvent.click(screen.getByRole('button', {name: 'Community calls options'}));
            fireEvent.keyDown(document, {key: 'Escape'});
            expect(screen.queryByRole('menu')).not.toBeInTheDocument();

            fireEvent.click(screen.getByRole('button', {name: 'Community calls options'}));
            fireEvent.mouseDown(document.body);
            expect(screen.queryByRole('menu')).not.toBeInTheDocument();
        });

        test('the layer and its menu show without a calendar account when feeds are allowed', async () => {
            mocked.getAccount.mockResolvedValue(null);
            mocked.getConfig.mockResolvedValue({reminders_enabled: true, allow_insecure_connections: false, calendar_feeds_enabled: true});
            renderPanel();
            await screen.findByText('Standup sync');

            expect(screen.getByRole('button', {name: 'Community calls'})).toBeInTheDocument();
            expect(screen.getByRole('button', {name: 'Community calls options'})).toBeInTheDocument();
        });

        test.each([
            ['turned off', false],
            ['not said (an older server)', undefined],
        ])('there\'s no Subscribe when feeds are %s', async (_, enabled) => {
            mocked.getConfig.mockResolvedValue({reminders_enabled: true, allow_insecure_connections: false, calendar_feeds_enabled: enabled});
            renderPanel();
            await screen.findByText('Standup sync');

            expect(screen.getByRole('button', {name: 'Community calls'})).toBeInTheDocument();
            expect(screen.queryByRole('button', {name: 'Community calls options'})).not.toBeInTheDocument();
            expect(screen.queryByText('Subscribe…')).not.toBeInTheDocument();
        });

        test('a failure to read them is said, without hiding the personal events', async () => {
            mocked.getCommunityCalls.mockRejectedValue(new Error('boom'));
            renderPanel();

            expect(await screen.findByText('Couldn’t read Community calls: boom')).toBeInTheDocument();
            expect(await screen.findByTestId('calendar-day-empty')).toBeInTheDocument();
        });
    });
});
