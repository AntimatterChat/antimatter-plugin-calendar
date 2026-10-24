// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import ReactDOM from 'react-dom';
import {useIntl} from 'react-intl';

import {am, own} from '../class_names';
import * as client from '../client';
import {isFusionUI} from '../web_ui';

import {fusionLayer} from './fusion/fusion_ui';
import {messages} from './messages';

type Props = {
    teamId: string;
    onClose: () => void;
};

// What the dialog shows: the link while it's being read or made, the new link (shown once), or,
// for a link made before, only when (the server keeps a hash of it, not the link).
type State =
    {kind: 'loading'} |
    {kind: 'link'; url: string} |
    {kind: 'made'; createdAt: number} |
    {kind: 'error'; message: string};

// SubscribeDialog is the "Subscribe…" dialog of the Community calls layer: the person's private
// calendar link (ICS feed) of the community's calls, to add to their own calendar app, with Copy
// and Reset link (which asks first, in the dialog itself). Opening it makes the link if the person
// has none yet.
export default function SubscribeDialog({teamId, onClose}: Props) {
    const {formatMessage, formatDate} = useIntl();
    const [state, setState] = useState<State>({kind: 'loading'});
    const [confirming, setConfirming] = useState(false);
    const [busy, setBusy] = useState(false);
    const [copied, setCopied] = useState<'copied' | 'select' | ''>('');
    const input = useRef<HTMLInputElement>(null);

    useEffect(() => {
        let cancelled = false;
        (async () => {
            try {
                const status = await client.getFeed(teamId);
                if (status.exists) {
                    if (!cancelled) {
                        setState({kind: 'made', createdAt: status.created_at || 0});
                    }
                    return;
                }
                const link = await client.createFeed(teamId);
                if (!cancelled) {
                    setState({kind: 'link', url: client.feedURL(link)});
                }
            } catch (err) {
                if (!cancelled) {
                    setState({kind: 'error', message: (err as Error).message});
                }
            }
        })();
        return () => {
            cancelled = true;
        };
    }, [teamId]);

    useEffect(() => {
        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                e.stopPropagation();
                if (confirming) {
                    setConfirming(false);
                } else {
                    onClose();
                }
            }
        };
        document.addEventListener('keydown', onKeyDown, true);
        return () => document.removeEventListener('keydown', onKeyDown, true);
    }, [confirming, onClose]);

    const selectLink = () => {
        input.current?.focus();
        input.current?.select();
    };

    const copy = async () => {
        if (state.kind !== 'link') {
            return;
        }
        try {
            if (!navigator.clipboard?.writeText) {
                throw new Error('no clipboard');
            }
            await navigator.clipboard.writeText(state.url);
            setCopied('copied');
        } catch {
            // The browser didn't let us: select it, for the person to copy.
            selectLink();
            setCopied('select');
        }
    };

    const reset = async () => {
        setBusy(true);
        try {
            const link = await client.createFeed(teamId);
            setState({kind: 'link', url: client.feedURL(link)});
            setCopied('');
        } catch (err) {
            setState({kind: 'error', message: (err as Error).message});
        } finally {
            setBusy(false);
            setConfirming(false);
        }
    };

    let body: React.ReactNode = null;
    switch (state.kind) {
    case 'loading':
        body = <p className={am('lead')}>{formatMessage(messages.loading)}</p>;
        break;
    case 'link':
        body = (
            <label
                className={am('field')}
                htmlFor='amc-feed-url'
            >
                <span>{formatMessage(messages.subscribeLink)}</span>
                <span className={own('feed-row')}>
                    <input
                        id='amc-feed-url'
                        ref={input}
                        type='text'
                        readOnly={true}
                        className={own('input', 'feed-url')}
                        value={state.url}
                        onFocus={(e) => e.target.select()}
                    />
                    <button
                        type='button'
                        className={am('btn', 'primary')}
                        onClick={copy}
                    >
                        {formatMessage(copied === 'copied' ? messages.copied : messages.copy)}
                    </button>
                </span>
                {copied === 'select' && <small>{formatMessage(messages.copySelect)}</small>}
            </label>
        );
        break;
    case 'made':
        body = (
            <p className={am('lead')}>
                {formatMessage(messages.subscribeHidden, {date: formatDate(state.createdAt, {dateStyle: 'medium'})})}
            </p>
        );
        break;
    case 'error':
        body = <div className={own('error')}>{state.message}</div>;
        break;
    }

    const layer = (isFusionUI() && fusionLayer()) || document.body;
    return ReactDOM.createPortal(
        <div className={own('root', {fusion: isFusionUI(), classic: !isFusionUI()})}>
            <div
                className={am('scrim')}
                onMouseDown={(e) => {
                    if (e.target === e.currentTarget) {
                        onClose();
                    }
                }}
            >
                <div
                    className={am('modal', 'small')}
                    role='dialog'
                    aria-modal='true'
                    aria-label={formatMessage(messages.subscribeTitle)}
                >
                    <div className={am('pane')}>
                        <h2>{formatMessage(messages.subscribeTitle)}</h2>
                        <p className={am('lead')}>{formatMessage(messages.subscribeLead)}</p>
                        {body}
                        <p
                            className={own('feed-warning')}
                            data-testid='feed-warning'
                        >
                            {formatMessage(messages.subscribeWarning)}
                        </p>
                        {confirming ? (
                            <div
                                className={own('feed-confirm')}
                                role='alert'
                            >
                                <p>{formatMessage(messages.resetConfirm)}</p>
                                <div className={am('modal-actions')}>
                                    <button
                                        type='button'
                                        className={am('btn')}
                                        disabled={busy}
                                        onClick={() => setConfirming(false)}
                                    >
                                        {formatMessage(messages.cancel)}
                                    </button>
                                    <button
                                        type='button'
                                        className={am('btn', 'danger')}
                                        disabled={busy}
                                        onClick={reset}
                                    >
                                        {formatMessage(messages.resetLink)}
                                    </button>
                                </div>
                            </div>
                        ) : (
                            <div className={am('modal-actions')}>
                                {state.kind !== 'loading' && (
                                    <button
                                        type='button'
                                        className={am('btn')}
                                        onClick={() => setConfirming(true)}
                                    >
                                        {formatMessage(messages.resetLink)}
                                    </button>
                                )}
                                <span className={own('grow')}/>
                                <button
                                    type='button'
                                    className={am('btn')}
                                    onClick={onClose}
                                >
                                    {formatMessage(messages.close)}
                                </button>
                            </div>
                        )}
                    </div>
                </div>
            </div>
        </div>,
        layer,
    );
}
