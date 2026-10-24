// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {act, fireEvent, render, screen} from '@testing-library/react';
import React from 'react';
import {IntlProvider} from 'react-intl';

import type {Channel} from '@mattermost/types/channels';

import * as client from '../client';
import type {NextCall} from '../client';
import {meetingChanged} from '../meeting_events';

import NextCallChip from './next_call_chip';

jest.mock('react-redux', () => ({useSelector: (selector: (state: object) => unknown) => selector({})}));
jest.mock('mattermost-redux/selectors/entities/teams', () => ({getCurrentTeam: () => ({id: 'team1', name: 'lumen'})}));
jest.mock('../client', () => ({
    ...jest.requireActual('../client'),
    getNextCall: jest.fn(),
}));

const mocked = client as jest.Mocked<typeof client>;

const MIN = 60000;
const NOW = new Date(2026, 9, 26, 12, 0).getTime(); // Monday 26 October 2026, 12:00 local

const channel = (overrides: Partial<Channel> = {}) => ({id: 'channel1', type: 'O', name: 'standup', display_name: 'Standup', ...overrides} as Channel);
const call = (start: number, overrides: Partial<NextCall> = {}): NextCall => ({
    meeting_id: 'meeting1',
    post_id: 'post1',
    summary: 'Weekly sync',
    start,
    end: start + 30 * MIN,
    live: false,
    recurring: false,
    ...overrides,
});

const renderChip = (c: Channel = channel()) => render(
    <IntlProvider locale='en'>
        <NextCallChip channel={c}/>
    </IntlProvider>,
);

// flush lets the mocked request resolve and React render it.
async function flush() {
    await act(async () => {
        await Promise.resolve();
    });
}

async function advance(ms: number) {
    await act(async () => {
        jest.advanceTimersByTime(ms);
        await Promise.resolve();
    });
}

// advanceMinutes moves the clock on a minute at a time, letting the chip render (and set its next
// timer) after each, as a real clock would.
async function advanceMinutes(minutes: number) {
    for (let i = 0; i < minutes; i++) {
        // eslint-disable-next-line no-await-in-loop
        await advance(MIN);
    }
}

beforeEach(() => {
    jest.useFakeTimers();
    jest.setSystemTime(NOW);
});

afterEach(() => {
    jest.useRealTimers();
    jest.clearAllMocks();
    delete window.antimatterCalls;
    delete window.WebappUtils;
});

describe('NextCallChip', () => {
    test('shows nothing in a direct or group message, without asking the server', async () => {
        const {container, rerender} = renderChip(channel({type: 'D'}));
        await flush();
        expect(container).toBeEmptyDOMElement();

        rerender(
            <IntlProvider locale='en'>
                <NextCallChip channel={channel({type: 'G'})}/>
            </IntlProvider>,
        );
        await flush();
        expect(container).toBeEmptyDOMElement();
        expect(mocked.getNextCall).not.toHaveBeenCalled();
    });

    test('shows nothing when there\'s no call within 7 days', async () => {
        mocked.getNextCall.mockResolvedValue(null);
        const {container} = renderChip();
        await flush();
        expect(mocked.getNextCall).toHaveBeenCalledWith('channel1');
        expect(container).toBeEmptyDOMElement();
    });

    test('says the day and time, opens the post, then counts down the last hour, then goes live and away', async () => {
        const start = NOW + (2 * 60 * MIN); // Monday 14:00
        mocked.getNextCall.mockResolvedValue(call(start));
        const push = jest.fn();
        window.WebappUtils = {browserHistory: {push}};
        window.antimatterCalls = {version: 1, selectors: {isCallsEnabled: () => true, getCall: () => null}, join: jest.fn(() => Promise.resolve())};
        renderChip();
        await flush();

        const chip = screen.getByRole('button', {name: /^Next call: Mon .* · Weekly sync$/});
        expect(chip).toHaveAttribute('title', 'Open the call’s post');
        fireEvent.click(chip);
        expect(push).toHaveBeenCalledWith('/lumen/pl/post1');

        // The last hour: "in N min", minute by minute.
        await advance(60 * MIN);
        expect(screen.getByRole('button', {name: 'Next call: in 60 min · Weekly sync'})).toBeInTheDocument();
        await advance(MIN);
        expect(screen.getByRole('button', {name: 'Next call: in 59 min · Weekly sync'})).toBeInTheDocument();

        // Live, with Join.
        await advanceMinutes(59);
        expect(screen.getByRole('button', {name: 'Live · Weekly sync'})).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', {name: 'Join'}));
        expect(window.antimatterCalls?.join).toHaveBeenCalledWith('channel1', {title: 'Weekly sync'});

        // Over: gone, and the next one is read, a minute after the last read at most.
        await advance(30 * MIN);
        expect(screen.queryByRole('button', {name: /Weekly sync/})).not.toBeInTheDocument();
        const reads = mocked.getNextCall.mock.calls.length;
        await advance(MIN);
        expect(mocked.getNextCall.mock.calls.length).toBe(reads + 1);
    });

    test('live without the Calls app: the chip, but no Join', async () => {
        mocked.getNextCall.mockResolvedValue(call(NOW - 5 * MIN, {live: true}));
        renderChip();
        await flush();
        expect(screen.getByRole('button', {name: 'Live · Weekly sync'})).toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Join'})).not.toBeInTheDocument();
    });

    test('reads again on a switch to another channel, and drops the answer for the one before', async () => {
        let answerFirst: (c: NextCall | null) => void = () => {};
        mocked.getNextCall.mockReturnValueOnce(new Promise((resolve) => {
            answerFirst = resolve;
        }));
        mocked.getNextCall.mockResolvedValueOnce(call(NOW + 3 * 60 * MIN, {summary: 'Other channel\'s call'}));
        const {rerender} = renderChip();

        rerender(
            <IntlProvider locale='en'>
                <NextCallChip channel={channel({id: 'channel2'})}/>
            </IntlProvider>,
        );
        await flush();
        expect(mocked.getNextCall).toHaveBeenLastCalledWith('channel2');

        // The first channel's answer arrives late: ignored.
        answerFirst(call(NOW + 60 * MIN * 2, {summary: 'First channel\'s call'}));
        await flush();
        expect(screen.getByRole('button', {name: /Other channel's call$/})).toBeInTheDocument();
        expect(screen.queryByRole('button', {name: /First channel's call$/})).not.toBeInTheDocument();
    });

    test('reads again when one of this channel\'s calls changes, not another channel\'s', async () => {
        mocked.getNextCall.mockResolvedValue(null);
        renderChip();
        await flush();
        expect(mocked.getNextCall).toHaveBeenCalledTimes(1);

        mocked.getNextCall.mockResolvedValue(call(NOW + 3 * 60 * MIN));
        act(() => meetingChanged('meeting1', 'channel1'));
        await flush();
        expect(mocked.getNextCall).toHaveBeenCalledTimes(2);
        expect(screen.getByRole('button', {name: /Weekly sync$/})).toBeInTheDocument();

        act(() => meetingChanged('meeting9', 'channel9'));
        await flush();
        expect(mocked.getNextCall).toHaveBeenCalledTimes(2);
    });

    test('never reads the server more than once a minute on its own', async () => {
        mocked.getNextCall.mockResolvedValue(null);
        renderChip();
        await flush();
        expect(mocked.getNextCall).toHaveBeenCalledTimes(1);

        // Nothing to show: only the slow refresh, every 15 minutes.
        await advance(14 * MIN);
        expect(mocked.getNextCall).toHaveBeenCalledTimes(1);
        await advance(MIN);
        expect(mocked.getNextCall).toHaveBeenCalledTimes(2);
    });

    test('a call that ended just after it was read waits a minute before reading the next', async () => {
        // The server and this clock disagree: the call it returned is already over here.
        mocked.getNextCall.mockResolvedValue(call(NOW - 31 * MIN));
        renderChip();
        await flush();
        expect(mocked.getNextCall).toHaveBeenCalledTimes(1);

        await advance(59 * 1000);
        expect(mocked.getNextCall).toHaveBeenCalledTimes(1);
        await advance(1000);
        expect(mocked.getNextCall).toHaveBeenCalledTimes(2);
    });
});
