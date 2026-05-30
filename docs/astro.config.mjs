// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://astro.build/config
export default defineConfig({
  site: 'https://docs.dploy.dev',
  integrations: [
    starlight({
      title: 'Dploy',
      logo: {
        src: './src/assets/dploy.svg',
        alt: 'Dploy',
      },
      favicon: '/favicon.ico',
      social: [
        { icon: 'github', label: 'GitHub', href: 'https://github.com/AYDEV-FR/dploy' },
      ],
      editLink: {
        baseUrl: 'https://github.com/AYDEV-FR/dploy/edit/main/docs/',
      },
      sidebar: [
        {
          label: 'Getting Started',
          items: [
            { label: 'Introduction', link: '/' },
            { label: 'Quick Start', link: '/quick-start/' },
            { label: 'Installation', link: '/installation/' },
            { label: 'Configuration', link: '/configuration/' },
          ],
        },
        {
          label: 'Concepts',
          items: [
            { label: 'Architecture', link: '/concepts/architecture/' },
            { label: 'Templates & Instances', link: '/concepts/templates/' },
          ],
        },
        {
          label: 'Guides',
          items: [
            { label: 'Web UI', link: '/guides/web-ui/' },
          ],
        },
        {
          label: 'API Reference',
          items: [
            { label: 'Overview', link: '/api/overview/' },
            { label: 'Endpoints', link: '/api/endpoints/' },
          ],
        },
        {
          label: 'Deployment',
          items: [
            { label: 'OIDC Providers', link: '/deployment/oidc-providers/' },
            { label: 'TLS Certificates', link: '/deployment/tls-certificates/' },
            { label: 'ExternalDNS (bare metal)', link: '/deployment/external-dns/' },
            { label: 'Security Considerations', link: '/deployment/security-considerations/' },
          ],
        },
        {
          label: 'Development',
          link: '/development/',
        },
      ],
    }),
  ],
});
