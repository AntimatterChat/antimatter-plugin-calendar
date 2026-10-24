// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {render, screen} from '@testing-library/react';
import React from 'react';
import {IntlProvider} from 'react-intl';

import type {Occurrence} from '../client';

import EventRow from './event_row';
import Week from './week';

jest.mock('react-redux', () => ({useSelector: (selector: (state: object) => unknown) => selector({})}));
jest.mock('mattermost-redux/selectors/entities/teams', () => ({getCurrentTeam: () => null}));

const setWebUI = (ui?: 'classic' | 'fusion') => {
    window.antimatterWebUI = ui;
    if (ui) {
        document.documentElement.dataset.amWebUi = ui;
    } else {
        delete document.documentElement.dataset.amWebUi;
    }
};

const event: Occurrence = {
    id: '/cal/work/sync.ics#1',
    calendar: '/cal/work/',
    path: '/cal/work/sync.ics',
    etag: 'e1',
    uid: 'sync',
    summary: 'Weekly sync with the design team',
    all_day: false,
    start: Date.parse('2026-10-12T12:00:00Z'),
    end: Date.parse('2026-10-12T12:30:00Z'),
    recurrence: 'weekly',
    recurrence_id: Date.parse('2026-10-12T12:00:00Z'),
};

const renderBoth = () => render(
    <IntlProvider locale='en'>
        <EventRow
            event={event}
            color='#FB7185'
            now={false}
            onOpen={jest.fn()}
        />
        <Week
            weekStart={new Date(2026, 9, 12)}
            events={[{...event, start: new Date(2026, 9, 12, 12).getTime(), end: new Date(2026, 9, 12, 13).getTime()}]}
            calendars={[{id: '/cal/work/', name: 'Work', read_only: false}]}
            now={Date.parse('2026-10-01T00:00:00Z')}
            onOpen={jest.fn()}
            onDay={jest.fn()}
        />
    </IntlProvider>,
);

afterEach(() => setWebUI(undefined));

describe('the tooltips of the events', () => {
    test('under Fusion, are Fusion\'s: the repeat mark (still named) and the week\'s truncated titles', () => {
        setWebUI('fusion');
        renderBoth();
        const repeat = screen.getByRole('img', {name: 'Repeats'});
        expect(repeat).toHaveAttribute('data-am-tip', 'Repeats');
        expect(repeat).not.toHaveAttribute('title');

        const chip = document.querySelector('button.amc-chip');
        expect(chip).toHaveAttribute('data-am-tip', 'Weekly sync with the design team');
        expect(document.querySelector('[title]')).toBeNull();
    });

    test('under the classic UI, stay the browser\'s', () => {
        setWebUI('classic');
        renderBoth();
        expect(screen.getByRole('img', {name: 'Repeats'})).toHaveAttribute('title', 'Repeats');
        expect(document.querySelector('[data-am-tip]')).toBeNull();
    });
});

describe('a community call\'s row', () => {
    const call: Occurrence = {
        ...event,
        id: '@community:meeting1:1',
        calendar: '@community',
        path: '',
        recurrence: '',
        link: {channel_id: 'channel1', kind: 'call', channel_name: 'standup', display_name: 'Standup', channel_type: 'O'},
        community: {
            meeting_id: 'meeting1',
            post_id: 'post1',
            channel_id: 'channel1',
            channel_name: 'standup',
            channel_display_name: 'Standup',
            channel_type: 'O',
            organizer_id: 'organizer1',
            summary: event.summary,
            start: event.start,
            end: event.end,
            recurring: false,
            my_rsvp: 'no',
        },
    };

    test('says its channel and the person\'s own answer, and opens its post', () => {
        const onOpen = jest.fn();
        render(
            <IntlProvider locale='en'>
                <EventRow
                    event={call}
                    color='#14B8A6'
                    now={false}
                    onOpen={onOpen}
                />
            </IntlProvider>,
        );
        expect(screen.getByText(/30 min · No/)).toHaveTextContent('Standup');
        screen.getByRole('button', {name: 'Open the post of Weekly sync with the design team'}).click();
        expect(onOpen).toHaveBeenCalledWith(call);

        // Not on now: no Join (or Open) button.
        expect(screen.queryByRole('button', {name: 'Open'})).not.toBeInTheDocument();
    });

    test('offers its join button while it\'s on', () => {
        render(
            <IntlProvider locale='en'>
                <EventRow
                    event={call}
                    color='#14B8A6'
                    now={true}
                    onOpen={jest.fn()}
                />
            </IntlProvider>,
        );
        expect(screen.getByRole('button', {name: 'Open'})).toBeInTheDocument();
    });
});
