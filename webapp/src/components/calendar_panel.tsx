// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import {useIntl} from 'react-intl';

import {am, own} from '../class_names';
import * as client from '../client';
import type {Account, Calendar, Occurrence} from '../client';
import {addDays, startOfDay, startOfWeek} from '../utils';
import {isFusionUI} from '../web_ui';

import AccountForm from './account_form';
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

// CalendarPanel is the calendar app, shown in the right-hand panel.
export default function CalendarPanel() {
    const {formatMessage} = useIntl();
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
    const [error, setError] = useState('');

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

    useEffect(() => {
        const interval = window.setInterval(() => setNow(Date.now()), CLOCK_INTERVAL);
        return () => window.clearInterval(interval);
    }, []);

    const step = (direction: number) => setDay((d) => addDays(d, direction * (mode === 'week' ? 7 : 1)));
    const reload = () => setReloadKey((k) => k + 1);
    const calendarName = (id: string) => calendars.find((c) => c.id === id)?.name || id;

    let content: React.ReactNode;
    if (!loaded) {
        content = (
            <div className={am('empty')}>
                {error || formatMessage(messages.loading)}
                {error && (
                    <>
                        {' '}
                        <button
                            className={own('link')}
                            onClick={loadAccount}
                        >
                            {formatMessage(messages.retry)}
                        </button>
                    </>
                )}
            </div>
        );
    } else if (!account || view.kind === 'settings') {
        content = (
            <AccountForm
                account={account}
                remindersEnabled={remindersEnabled}
                onSaved={(saved) => {
                    setAccount(saved);
                    setView({kind: 'calendar'});
                    reload();
                }}
                onCancel={account ? () => setView({kind: 'calendar'}) : null}
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
        const open = (occurrence: Occurrence) => setView({kind: 'editor', occurrence});
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
                        title={formatMessage(messages.newEvent)}
                        aria-label={formatMessage(messages.newEvent)}
                        disabled={!calendars.some((c) => !c.read_only)}
                        onClick={() => setView({kind: 'editor', occurrence: null})}
                    >
                        <Icon name='plus'/>
                    </button>
                    <button
                        className={am('icon-btn')}
                        title={formatMessage(messages.refresh)}
                        aria-label={formatMessage(messages.refresh)}
                        onClick={reload}
                    >
                        <Icon name='refresh'/>
                    </button>
                    <button
                        className={am('icon-btn')}
                        title={formatMessage(messages.settings)}
                        aria-label={formatMessage(messages.settings)}
                        onClick={() => setView({kind: 'settings'})}
                    >
                        <Icon name='cog'/>
                    </button>
                </div>
                <div className={own('toolbar')}>
                    <button
                        className={am('icon-btn')}
                        title={formatMessage(messages.previous)}
                        aria-label={formatMessage(messages.previous)}
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
                        title={formatMessage(messages.next)}
                        aria-label={formatMessage(messages.next)}
                        onClick={() => step(1)}
                    >
                        <Icon name='right'/>
                    </button>
                </div>
                {error && <div className={own('error')}>{error}</div>}
                {failures.map((f) => (
                    <div
                        key={f.calendar}
                        className={own('error')}
                    >
                        {formatMessage(messages.calendarFailed, {calendar: calendarName(f.calendar), error: f.error})}
                    </div>
                ))}
                {events === null && !error && <div className={am('empty')}>{formatMessage(messages.loading)}</div>}
                {events && mode === 'agenda' && (
                    <Agenda
                        day={day}
                        events={events}
                        calendars={calendars}
                        now={now}
                        onOpen={open}
                    />
                )}
                {events && mode === 'week' && (
                    <Week
                        weekStart={startOfWeek(day)}
                        events={events}
                        calendars={calendars}
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

    return <div className={own('root', {fusion: isFusionUI(), classic: !isFusionUI()})}>{content}</div>;
}
