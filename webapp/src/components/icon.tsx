// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

import React from 'react';

import {am} from '../class_names';
import {isFusionUI} from '../web_ui';

// The icons of the Fusion mockup the calendar panel uses, as paths for the classic UI, which has no Fusion sprite.
const paths: Record<string, React.ReactNode> = {
    calendar: (<path d='M5 5h14a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2zM3 10h18M8 3v4M16 3v4M8 14h3v3H8z'/>),
    plus: (<path d='M12 5v14M5 12h14'/>),
    cog: (<path d='M15.2 12a3.2 3.2 0 1 1-6.4 0 3.2 3.2 0 0 1 6.4 0zM12 2.5v3M12 18.5v3M4.6 4.6l2.1 2.1M17.3 17.3l2.1 2.1M2.5 12h3M18.5 12h3M4.6 19.4l2.1-2.1M17.3 6.7l2.1-2.1'/>),
    speaker: (<path d='M4 10v4h4l5 4V6l-5 4H4zM16.5 9a4.5 4.5 0 0 1 0 6M19 6.5a8 8 0 0 1 0 11'/>),
    phone: (<path d='M5 4h3.5l2 5-2.5 1.5a11 11 0 0 0 5.5 5.5L15 13.5l5 2V19a2 2 0 0 1-2 2A16 16 0 0 1 3 6a2 2 0 0 1 2-2z'/>),
    hash: (<path d='M5 9h15M4 15h15M10 3 8 21M16 3l-2 18'/>),
    back: (<path d='M15 6l-6 6 6 6'/>),
    left: (<path d='M15 6l-6 6 6 6'/>),
    right: (<path d='M9 6l6 6-6 6'/>),
    repeat: (<path d='M17 2l4 4-4 4M3 11V9a3 3 0 0 1 3-3h15M7 22l-4-4 4-4M21 13v2a3 3 0 0 1-3 3H3'/>),
    refresh: (<path d='M20 11a8 8 0 0 0-14.6-4.5L4 8M4 4v4h4M4 13a8 8 0 0 0 14.6 4.5L20 16M20 20v-4h-4'/>),
    dots: (<path d='M5 12h.01M12 12h.01M19 12h.01'/>),
};

export type IconName = keyof typeof paths;

// The symbols of Fusion's icon sprite (#am-i-<name>) drawn under Fusion, and how they're turned: the sprite's chevron
// points down. Fusion's sprite has no repeat or refresh arrows, which keep their own drawing.
const sprite: Partial<Record<IconName, {name: string; turn?: number}>> = {
    calendar: {name: 'calendar'},
    plus: {name: 'plus'},
    cog: {name: 'cog'},
    speaker: {name: 'speaker'},
    phone: {name: 'phone'},
    hash: {name: 'hash'},
    back: {name: 'chev', turn: 90},
    left: {name: 'chev', turn: 90},
    right: {name: 'chev', turn: -90},
    dots: {name: 'dots'},
};

type Props = {
    name: IconName;
    size?: 'sm' | 'xs';
};

export default function Icon({name, size}: Props) {
    const symbol = isFusionUI() ? sprite[name] : null;
    if (symbol) {
        return (
            <svg
                className={am('ic', size)}
                aria-hidden='true'
                data-icon={name}
                style={symbol.turn ? {transform: `rotate(${symbol.turn}deg)`} : {}}
            >
                <use href={`#am-i-${symbol.name}`}/>
            </svg>
        );
    }
    return (
        <svg
            className={am('ic', size)}
            viewBox='0 0 24 24'
            aria-hidden='true'
            data-icon={name}
        >
            {paths[name]}
        </svg>
    );
}
