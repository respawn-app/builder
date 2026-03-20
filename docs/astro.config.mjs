import { defineConfig } from 'astro/config';
import sitemap from '@astrojs/sitemap';
import starlight from '@astrojs/starlight';
import remarkGfm from 'remark-gfm';

import { resolveDocsConfig } from './scripts/site-config.mjs';

const docsConfig = resolveDocsConfig();

export default defineConfig({
  output: 'static',
  site: docsConfig.siteUrl,
  base: docsConfig.basePath,
  integrations: [
    starlight({
      title: docsConfig.siteTitle,
      logo: {
        alt: docsConfig.siteTitle,
        src: './src/assets/logo.svg',
      },
      social: [
        {
          icon: 'github',
          label: 'GitHub',
          href: docsConfig.repoUrl,
        },
      ],
      sidebar: [
        {
          label: docsConfig.docsHomeLabel,
          link: docsConfig.docsHomePath,
        },
      ],
      editLink: {
        baseUrl: docsConfig.repoEditRootUrl,
      },
      customCss: ['./src/styles/custom.css'],
      markdown: {
        headingLinks: true,
      },
      components: {
        Header: './src/components/Header.astro',
        MobileMenuFooter: './src/components/MobileMenuFooter.astro',
        Footer: './src/components/Footer.astro',
        PageTitle: './src/components/PageTitle.astro',
        ThemeSelect: './src/components/ThemeSelect.astro',
      },
      expressiveCode: {
        themes: ['one-light', 'one-dark-pro'],
        useStarlightDarkModeSwitch: true,
        useStarlightUiThemeColors: true,
      },
      lastUpdated: false,
      pagination: true,
      favicon: '/favicon.svg',
      credits: false,
      disable404Route: true,
      head: [
        {
          tag: 'link',
          attrs: {
            rel: 'preconnect',
            href: 'https://fonts.googleapis.com',
          },
        },
        {
          tag: 'link',
          attrs: {
            rel: 'preconnect',
            href: 'https://fonts.gstatic.com',
            crossorigin: '',
          },
        },
        {
          tag: 'link',
          attrs: {
            rel: 'stylesheet',
            href:
              'https://fonts.googleapis.com/css2?family=Comfortaa:wght@400;500;600;700&family=Montserrat+Alternates:wght@500;600;700&display=swap',
          },
        },
        {
          tag: 'meta',
          attrs: {
            name: 'robots',
            content: 'index,follow,max-image-preview:large,max-snippet:-1,max-video-preview:-1',
          },
        },
        {
          tag: 'meta',
          attrs: {
            name: 'googlebot',
            content: 'index,follow,max-image-preview:large,max-snippet:-1,max-video-preview:-1',
          },
        },
      ],
    }),
    sitemap(),
  ],
  markdown: {
    remarkPlugins: [remarkGfm],
  },
});
