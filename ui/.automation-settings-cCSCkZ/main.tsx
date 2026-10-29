import '../src/fonts.css';import '../src/styles.css';import '../src/interaction-system.css';import '../src/settings-system.css';import '../src/desktop-system.css';import '../src/conversation-workspace.css';import './foundations.css';import '../src/v2-app.css';import '../src/sender-groups.css';import '../src/settings/settings-components.css';import '../src/settings/settings-polish.css';import '../src/web-tool-steps.css';import '../src/web-mention.css';import '../src/web-file-drop.css';import '../src/web-sidebar-list.css';import '../src/sidebar-pins.css';import '../src/sidebar-rail.css';import '../src/chat-header.css';import '../src/progressive-blur.css';import '../src/context-card.css';import '../src/focus-ring.css';
import React from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady,setLanguage} from '../src/i18n';
import {AutomationSettings} from '../src/AutomationSettings';
import {AllowancesPage} from '../src/settings/SettingsPages';
import {TimezoneProvider} from '../src/UserTimezone';
const query=new URLSearchParams(location.search);
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><div className="workspace"><div className="settings-page-body" data-page={query.get('page')||'automation'}>{query.get('page')==='permissions'?<AllowancesPage openTab={tab=>{(window as any).__openTab=tab}}/>:<AutomationSettings onOpenPermissions={()=>{(window as any).__openTab='permissions'}}/>}</div></div></TimezoneProvider>));
