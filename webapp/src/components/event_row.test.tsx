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
