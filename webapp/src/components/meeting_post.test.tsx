// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import {act, render, screen, waitFor} from '@testing-library/react';
import React from 'react';
import {IntlProvider} from 'react-intl';

import type {Post} from '@mattermost/types/posts';

import * as client from '../client';
import type {Meeting} from '../client';
import {meetingChanged} from '../meeting_events';

import MeetingPost from './meeting_post';

jest.mock('../client', () => ({
    ...jest.requireActual('../client'),
    getMeeting: jest.fn(),
    setRSVP: jest.fn(),
    cancelMeeting: jest.fn(),
}));

jest.mock('react-redux', () => ({useSelector: (selector: (state: object) => unknown) => selector({})}));
jest.mock('mattermost-redux/selectors/entities/teams', () => ({getCurrentTeam: () => null}));
jest.mock('mattermost-redux/selectors/entities/channels', () => ({
    getChannel: () => ({id: 'channel1', name: 'standup', display_name: 'Standup', type: 'O'}),
}));
jest.mock('mattermost-redux/selectors/entities/users', () => ({getCurrentUserId: () => 'organizer1'}));

const mocked = client as jest.Mocked<typeof client>;

function testMeeting(overrides: Partial<Meeting> = {}): Meeting {
    return {
        id: 'meeting1',
        channel_id: 'channel1',
        post_id: 'post1',
        organizer_id: 'organizer1',
        summary: 'Team sync',
        description: '',
        start: Date.parse('2026-10-27T14:00:00Z'),
        end: Date.parse('2026-10-27T14:30:00Z'),
        sequence: 0,
        cancelled: false,
        created_at: Date.parse('2026-10-20T00:00:00Z'),
        updated_at: Date.parse('2026-10-20T00:00:00Z'),
        can_cancel: true,
        ...overrides,
    };
}

function testPost(): Post {
    return {
        id: 'post1',
        channel_id: 'channel1',
        edit_at: 0,
        props: {meeting_id: 'meeting1'},
    } as unknown as Post;
}

function renderPost(post: Post = testPost()) {
    return render(
        <IntlProvider locale='en'>
            <MeetingPost post={post}/>
        </IntlProvider>,
    );
}

afterEach(() => jest.clearAllMocks());

describe('MeetingPost', () => {
    test('shows the title, the time and (as the organiser) Edit and Cancel', async () => {
        mocked.getMeeting.mockResolvedValue(testMeeting());
        renderPost();

        expect(await screen.findByText('Team sync')).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Edit'})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Cancel the call'})).toBeInTheDocument();
    });

    test('someone who isn\'t the organiser sees RSVP, but not Edit or Cancel', async () => {
        mocked.getMeeting.mockResolvedValue(testMeeting({organizer_id: 'someone-else'}));
        renderPost();

        expect(await screen.findByText('Team sync')).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Going'})).toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Edit'})).not.toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Cancel the call'})).not.toBeInTheDocument();
    });

    test('RSVP sends the choice and shows it pressed', async () => {
        mocked.getMeeting.mockResolvedValue(testMeeting());
        mocked.setRSVP.mockResolvedValue(testMeeting({rsvps: {organizer1: {status: 'maybe', updated_at: 1}}}));
        renderPost();
        await screen.findByText('Team sync');

        const maybe = screen.getByRole('button', {name: 'Maybe'});
        expect(maybe).toHaveAttribute('aria-pressed', 'false');
        act(() => {
            maybe.click();
        });

        await waitFor(() => expect(maybe).toHaveAttribute('aria-pressed', 'true'));
        expect(mocked.setRSVP).toHaveBeenCalledWith('meeting1', 'maybe');
    });

    test('a cancelled meeting shows the badge and hides Join, RSVP and add to calendar', async () => {
        mocked.getMeeting.mockResolvedValue(testMeeting({cancelled: true}));
        renderPost();

        expect(await screen.findByText('Cancelled')).toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Going'})).not.toBeInTheDocument();
        expect(screen.queryByRole('link', {name: 'Add to calendar'})).not.toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Cancel the call'})).not.toBeInTheDocument();
    });

    test('reads the meeting again when the server says it changed', async () => {
        mocked.getMeeting.mockResolvedValue(testMeeting());
        renderPost();
        await screen.findByText('Team sync');

        mocked.getMeeting.mockResolvedValue(testMeeting({summary: 'Team sync (moved)'}));
        act(() => meetingChanged('meeting1'));
        expect(await screen.findByText('Team sync (moved)')).toBeInTheDocument();

        // A different meeting changing doesn't trigger a reload of this one.
        mocked.getMeeting.mockClear();
        act(() => meetingChanged('some-other-meeting'));
        expect(mocked.getMeeting).not.toHaveBeenCalled();
    });
});
