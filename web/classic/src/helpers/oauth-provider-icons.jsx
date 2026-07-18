/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import { Layers } from 'lucide-react';
import {
  SiAtlassian,
  SiAuth0,
  SiAuthentik,
  SiBitbucket,
  SiDiscord,
  SiDropbox,
  SiFacebook,
  SiGitea,
  SiGithub,
  SiGitlab,
  SiGoogle,
  SiKeycloak,
  SiNextcloud,
  SiNotion,
  SiOkta,
  SiOpenid,
  SiReddit,
  SiTelegram,
  SiTwitch,
  SiWechat,
  SiX,
} from 'react-icons/si';
import { FaLinkedin, FaSlack } from 'react-icons/fa';

const oauthProviderIconMap = {
  github: SiGithub,
  gitlab: SiGitlab,
  gitea: SiGitea,
  google: SiGoogle,
  discord: SiDiscord,
  facebook: SiFacebook,
  linkedin: FaLinkedin,
  x: SiX,
  twitter: SiX,
  slack: FaSlack,
  telegram: SiTelegram,
  wechat: SiWechat,
  keycloak: SiKeycloak,
  nextcloud: SiNextcloud,
  authentik: SiAuthentik,
  openid: SiOpenid,
  okta: SiOkta,
  auth0: SiAuth0,
  atlassian: SiAtlassian,
  bitbucket: SiBitbucket,
  notion: SiNotion,
  twitch: SiTwitch,
  reddit: SiReddit,
  dropbox: SiDropbox,
};

function isHttpUrl(value) {
  return /^https?:\/\//i.test(value || '');
}

function isSimpleEmoji(value) {
  if (!value) return false;
  const trimmed = String(value).trim();
  return trimmed.length > 0 && trimmed.length <= 4 && !isHttpUrl(trimmed);
}

function normalizeOAuthIconKey(raw) {
  return raw
    .trim()
    .toLowerCase()
    .replace(/^ri:/, '')
    .replace(/^react-icons:/, '')
    .replace(/^si:/, '');
}

/**
 * Render custom OAuth provider icon with react-icons or URL/emoji fallback.
 * Supported formats:
 * - react-icons simple key: github / gitlab / google / keycloak
 * - prefixed key: ri:github / si:github
 * - full URL image: https://example.com/logo.png
 * - emoji: 🐱
 */
export function getOAuthProviderIcon(
  iconName,
  size = 20,
  FallbackAvatar = null,
) {
  const raw = String(iconName || '').trim();
  const iconSize = Number(size) > 0 ? Number(size) : 20;

  if (!raw) {
    return <Layers size={iconSize} color='var(--semi-color-text-2)' />;
  }

  if (isHttpUrl(raw)) {
    return (
      <img
        src={raw}
        alt='provider icon'
        width={iconSize}
        height={iconSize}
        style={{ borderRadius: 4, objectFit: 'cover' }}
      />
    );
  }

  if (isSimpleEmoji(raw)) {
    return (
      <span
        style={{
          width: iconSize,
          height: iconSize,
          lineHeight: `${iconSize}px`,
          textAlign: 'center',
          display: 'inline-block',
          fontSize: Math.max(Math.floor(iconSize * 0.8), 14),
        }}
      >
        {raw}
      </span>
    );
  }

  const key = normalizeOAuthIconKey(raw);
  const IconComp = oauthProviderIconMap[key];
  if (IconComp) {
    return <IconComp size={iconSize} />;
  }

  const fallbackText = raw.charAt(0).toUpperCase();
  if (FallbackAvatar) {
    return (
      <FallbackAvatar size='extra-extra-small'>{fallbackText}</FallbackAvatar>
    );
  }

  return <span>{fallbackText}</span>;
}
