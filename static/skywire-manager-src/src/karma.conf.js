// Karma configuration file, see link for more information
// https://karma-runner.github.io/1.0/config/configuration-file.html

module.exports = function (config) {
  config.set({
    basePath: '',
    // No @angular-devkit/build-angular framework or plugin: @angular/build:karma
    // supplies its own, and that package is no longer installed.
    frameworks: ['jasmine'],
    plugins: [
      require('karma-jasmine'),
      require('karma-chrome-launcher'),
      require('karma-jasmine-html-reporter'),
      require('karma-coverage-istanbul-reporter')
    ],
    client: {
      clearContext: false // leave Jasmine Spec Runner output visible in browser
    },
    coverageIstanbulReporter: {
      dir: require('path').join(__dirname, '../coverage'),
      reports: ['html', 'lcovonly']
    },
    reporters: ['progress', 'kjhtml'],
    port: 9876,
    colors: true,
    logLevel: config.LOG_INFO,
    autoWatch: true,
    // ChromeHeadlessNoSandbox is the CI launcher: headless Chrome needs
    // --no-sandbox inside the GitHub Actions container (it runs as root, where
    // Chrome's sandbox refuses to start). Selected in CI via
    // `ng test --browsers=ChromeHeadlessNoSandbox --watch=false`; interactive
    // local runs keep the default headed 'Chrome'.
    customLaunchers: {
      ChromeHeadlessNoSandbox: {
        base: 'ChromeHeadless',
        flags: ['--no-sandbox', '--disable-gpu']
      }
    },
    browsers: ['Chrome'],
    singleRun: false
  });
};