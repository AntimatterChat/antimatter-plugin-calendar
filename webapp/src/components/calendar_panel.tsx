// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import {useIntl} from 'react-intl';
import {useSelector} from 'react-redux';

import {getCurrentTeam} from 'mattermost-redux/selectors/entities/teams';

import {am, own} from '../class_names';
import * as client from '../client';
import type {Account, Calendar, Occurrence} from '../client';
import {COMMUNITY_CALENDAR, communityLayer, openPost, toOccurrence} from '../community_calls';
import {onMeetingChanged} from '../meeting_events';
import {addDays, calendarColor, startOfDay, startOfWeek} from '../utils';
import {isFusionUI} from '../web_ui';

import AccountForm from './account_form';
import EmptyState from './fusion/empty_state';
import {iconTip} from './fusion/tip';
import Agenda, {UPCOMING_DAYS} from './agenda';
import EventEditor from './event_editor';
import Icon from './icon';
import {messages} from './messages';
import Week from './week';

type Mode = 'agenda' | 'week';

type View =
    {kind: 'calendar'} |
    {kind: 'editor'; occurrence: Occurrence | null} |
    {kind: 'settings'};

// HIDDEN_KEY keeps the calendars the user hid, in this browser.
const HIDDEN_KEY = 'antimatter-calendar-hidden';

function loadHidden(): string[] {
    try {
        const hidden = JSON.parse(window.localStorage.getItem(HIDDEN_KEY) || '[]');
        return Array.isArray(hidden) ? hidden : [];
    } catch {
        return [];
    }
}

function saveHidden(hidden: string[]) {
    try {
        window.localStorage.setItem(HIDDEN_KEY, JSON.stringify(hidden));
    } catch {
        // Not kept, then
    }
}

// Calendar's colour in the Fusion UI (its app rail icon), for Fusion's parts that take the app's colour (--am-cc).
export const APP_COLOR = '#FB7185';

// Events are read again every few minutes, and the current event highlighted every minute.
const REFRESH_INTERVAL = 5 * 60 * 1000;
const CLOCK_INTERVAL = 60 * 1000;

function range(mode: Mode, day: Date): [Date, Date] {
    if (mode === 'week') {
        const start = startOfWeek(day);
        return [start, addDays(start, 7)];
    }
    return [day, addDays(day, UPCOMING_DAYS + 1)];
}

// CalendarPanel is the calendar app, shown in the right-hand panel: the person's own calendars
// (once they connected an account) and the community's scheduled calls (with or without one).
export default function CalendarPanel() {
    const {formatMessage} = useIntl();
    const team = useSelector(getCurrentTeam);
    const teamId = team?.id || '';
    const [account, setAccount] = useState<Account | null>(null);
    const [loaded, setLoaded] = useState(false);
    const [remindersEnabled, setRemindersEnabled] = useState(true);
    const [calendars, setCalendars] = useState<Calendar[]>([]);
    const [mode, setMode] = useState<Mode>('agenda');
    const [day, setDay] = useState(() => startOfDay(new Date()));
    const [events, setEvents] = useState<Occurrence[] | null>(null);
    const [failures, setFailures] = useState<Array<{calendar: string; error: string}>>([]);
    const [view, setView] = useState<View>({kind: 'calendar'});
    const [now, setNow] = useState(() => Date.now());
    const [reloadKey, setReloadKey] = useState(0);
    const [hidden, setHidden] = useState<string[]>(loadHidden);
    const [error, setError] = useState('');
    const [communityEvents, setCommunityEvents] = useState<Occurrence[]>([]);
    const [communityError, setCommunityError] = useState('');
    const [callsChanged, setCallsChanged] = useState(0);

    const loadAccount = useCallback(async () => {
        setError('');
        try {
            const [loadedAccount, config] = await Promise.all([client.getAccount(), client.getConfig()]);
            setAccount(loadedAccount);
            setRemindersEnabled(config.reminders_enabled);
            setLoaded(true);
        } catch (err) {
            setError((err as Error).message);
        }
    }, []);

    useEffect(() => {
        loadAccount();
    }, [loadAccount]);

    useEffect(() => {
        if (!account) {
            return;
        }
        client.getCalendars().then(setCalendars).catch((err) => setError((err as Error).message));
    }, [account, reloadKey]);

    useEffect(() => {
        let cancelled = false;
        let interval = 0;
        const [from, to] = range(mode, day);
        const load = async () => {
            try {
                const result = await client.getEvents(from, to);
                if (!cancelled) {
                    setEvents(result.events);
                    setFailures(result.errors);
                    setError('');
                }
            } catch (err) {
                if (!cancelled) {
                    setError((err as Error).message);
                }
            }
        };
        if (account) {
            load();
            interval = window.setInterval(load, REFRESH_INTERVAL);
        }
        return () => {
            cancelled = true;
            window.clearInterval(interval);
        };
    }, [account, mode, day, reloadKey]);

    // The community's calls, read for the same days as the events, and again whenever the server
    // says a call changed.
    useEffect(() => onMeetingChanged(() => setCallsChanged((n) => n + 1)), []);
    useEffect(() => {
        let cancelled = false;
        let interval = 0;
        const [from, to] = range(mode, day);
        const load = async () => {
            try {
                const result = await client.getCommunityCalls(teamId, from, to);
                if (!cancelled) {
                    setCommunityEvents(result.occurrences.map(toOccurrence));
                    setCommunityError('');
                }
            } catch (err) {
                if (!cancelled) {
                    setCommunityError((err as Error).message);
                }
            }
        };
        if (teamId) {
            load();
            interval = window.setInterval(load, REFRESH_INTERVAL);
        } else {
            setCommunityEvents([]);
        }
        return () => {
            cancelled = true;
            window.clearInterval(interval);
        };
    }, [teamId, mode, day, reloadKey, callsChanged]);

    useEffect(() => {
        const interval = window.setInterval(() => setNow(Date.now()), CLOCK_INTERVAL);
        return () => window.clearInterval(interval);
    }, []);

    const step = (direction: number) => setDay((d) => addDays(d, direction * (mode === 'week' ? 7 : 1)));
    const reload = () => setReloadKey((k) => k + 1);
    const layers = teamId ? [...calendars, communityLayer(formatMessage(messages.communityCalls))] : calendars;
    const calendarName = (id: string) => layers.find((c) => c.id === id)?.name || id;
    const toggleCalendar = (id: string) => {
        const next = hidden.includes(id) ? hidden.filter((h) => h !== id) : [...hidden, id];
        setHidden(next);
        saveHidden(next);
    };

    // Without an account there are no personal events to wait for: only the community's calls.
    const personal = account ? events : [];
    const community = hidden.includes(COMMUNITY_CALENDAR) ? [] : communityEvents;
    const shown = personal ? [...personal.filter((e) => !hidden.includes(e.calendar)), ...community].sort((a, b) => a.start - b.start) : null;
    const allFailures = communityError ? [...failures, {calendar: COMMUNITY_CALENDAR, error: communityError}] : failures;

    let content: React.ReactNode;
    if (!loaded && !error) {
        content = (
            <EmptyState
                busy={true}
                text={formatMessage(messages.loading)}
                classicClassName={am('empty')}
            />
        );
    } else if (!loaded) {
        content = (
            <div className={am('empty')}>
                {error}
                {' '}
                <button
                    className={own('link')}
                    onClick={loadAccount}
                >
                    {formatMessage(messages.retry)}
                </button>
            </div>
        );
    } else if ((!account && !teamId) || view.kind === 'settings') {
        content = (
            <AccountForm
                account={account}
                remindersEnabled={remindersEnabled}
                onSaved={(saved) => {
                    setAccount(saved);
                    setView({kind: 'calendar'});
                    reload();
                }}
                onCancel={account || teamId ? () => setView({kind: 'calendar'}) : null}
                onDisconnected={() => {
                    setAccount(null);
                    setCalendars([]);
                    setEvents(null);
                    setView({kind: 'calendar'});
                }}
            />
        );
    } else if (view.kind === 'editor') {
        content = (
            <EventEditor
                calendars={calendars}
                occurrence={view.occurrence}
                day={day}
                onDone={() => {
                    setView({kind: 'calendar'});
                    reload();
                }}
                onCancel={() => setView({kind: 'calendar'})}
            />
        );
    } else {
        const open = (occurrence: Occurrence) => {
            if (occurrence.community) {
                openPost(team?.name, occurrence.community.post_id);
                return;
            }
            setView({kind: 'editor', occurrence});
        };
        content = (
            <div className={am('app-panel')}>
                <div className={own('toolbar')}>
                    <div
                        className={am('seg')}
                        role='tablist'
                    >
                        {(['agenda', 'week'] as Mode[]).map((m) => (
                            <button
                                key={m}
                                role='tab'
                                aria-selected={mode === m}
                                className={am({on: mode === m})}
                                onClick={() => setMode(m)}
                            >
                                {formatMessage(m === 'agenda' ? messages.agenda : messages.week)}
                            </button>
                        ))}
                    </div>
                    <span className={am('grow')}/>
                    <button
                        className={am('icon-btn')}
                        {...iconTip(formatMessage(messages.newEvent))}
                        disabled={!calendars.some((c) => !c.read_only)}
                        onClick={() => setView({kind: 'editor', occurrence: null})}
                    >
                        <Icon name='plus'/>
                    </button>
                    <button
                        className={am('icon-btn')}
                        {...iconTip(formatMessage(messages.refresh))}
                        onClick={reload}
                    >
                        <Icon name='refresh'/>
                    </button>
                    <button
                        className={am('icon-btn')}
                        {...iconTip(formatMessage(account ? messages.settings : messages.setupTitle))}
                        onClick={() => setView({kind: 'settings'})}
                    >
                        <Icon name='cog'/>
                    </button>
                </div>
                <div className={own('toolbar')}>
                    <button
                        className={am('icon-btn')}
                        {...iconTip(formatMessage(messages.previous))}
                        onClick={() => step(-1)}
                    >
                        <Icon name='left'/>
                    </button>
                    <button
                        className={am('chip-btn')}
                        onClick={() => setDay(startOfDay(new Date()))}
                    >
                        {formatMessage(messages.today)}
                    </button>
                    <button
                        className={am('icon-btn')}
                        {...iconTip(formatMessage(messages.next))}
                        onClick={() => step(1)}
                    >
                        <Icon name='right'/>
                    </button>
                </div>
                {error && <div className={own('error')}>{error}</div>}
                {layers.length > 1 && (
                    <div className={own('calendars')}>
                        {layers.map((c) => (
                            <button
                                key={c.id}
                                className={am('chip-btn', {on: !hidden.includes(c.id)})}
                                aria-pressed={!hidden.includes(c.id)}
                                onClick={() => toggleCalendar(c.id)}
                            >
                                <i
                                    className={own('dot')}
                                    style={{background: calendarColor(layers, c.id)}}
                                />
                                {c.name}
                            </button>
                        ))}
                    </div>
                )}
                {allFailures.map((f) => (
                    <div
                        key={f.calendar}
                        className={own('error')}
                    >
                        {formatMessage(messages.calendarFailed, {calendar: calendarName(f.calendar), error: f.error})}
                    </div>
                ))}
                {shown === null && !error && (
                    <EmptyState
                        busy={true}
                        text={formatMessage(messages.loading)}
                        classicClassName={am('empty')}
                    />
                )}
                {shown && mode === 'agenda' && (
                    <Agenda
                        day={day}
                        events={shown}
                        calendars={layers}
                        now={now}
                        onOpen={open}
                    />
                )}
                {shown && mode === 'week' && (
                    <Week
                        weekStart={startOfWeek(day)}
                        events={shown}
                        calendars={layers}
                        now={now}
                        onOpen={open}
                        onDay={(d) => {
                            setDay(d);
                            setMode('agenda');
                        }}
                    />
                )}
            </div>
        );
    }

    return (
        <div
            className={own('root', {fusion: isFusionUI(), classic: !isFusionUI()})}
            style={{'--am-cc': APP_COLOR} as React.CSSProperties}
        >
            {content}
        </div>
    );
}
