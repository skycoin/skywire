export const AppConfig = {
  /**
   * How many elements the short lists can have. If the list has more elements, a
   * link for opening the full list is shown.
   */
  maxShortListElements: 5,
  /**
   * How many elements the full lists can have per page.
   */
  maxFullListElements: 40,
  /**
   * How many ms the system will wait before retrying to get the data if there is an error.
   */
  connectionRetryDelay: 5000,

  /**
   * Available languages.
   */
  languages: [
    {
      code: 'en',
      name: 'English',
      iconName: 'en.png',
    },
    {
      code: 'es',
      name: 'Español',
      iconName: 'es.png',
    },
    {
      code: 'de',
      name: 'Deutsch',
      iconName: 'de.png',
    },
  ],
  /**
   * Default language.
   */
  defaultLanguage: 'en',

  /**
   * Sizes of the modal windows.
   */
  smallModalWidth: '480px',
  mediumModalWidth: '640px',
  largeModalWidth: '900px',

  /**
   * Vpn desktop client configuration.
   */
  vpn: {
    /**
     * If true, a hardcoded ip will be shown in the UI while in development mode.
     */
    hardcodedIpWhileDeveloping: true,
  },
};
