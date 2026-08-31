const path = require('path');
const webpack = require('webpack');

module.exports = (_env, argv) => ({
  entry: './src/index.tsx',
  mode: argv.mode === 'production' ? 'production' : 'development',
  devtool: 'source-map',
  resolve: {
    extensions: ['.tsx', '.ts', '.js'],
  },
  module: {
    rules: [
      { test: /\.tsx?$/, use: 'ts-loader', exclude: /node_modules/ },
    ],
  },
  output: {
    path: path.resolve(__dirname, 'dist'),
    publicPath: 'auto',
    clean: true,
  },
  plugins: [
    // Module Federation contract (spec section 10 / quick-start docs):
    // Superset requests exactly the "./index" exposed module. Container
    // name follows the publisher_camelCaseName convention from the
    // framework's own quick-start example.
    new webpack.container.ModuleFederationPlugin({
      name: 'acme_chFederation',
      filename: 'remoteEntry.js',
      exposes: {
        './index': './src/index.tsx',
      },
      shared: {
        react: { singleton: true, requiredVersion: false },
        'react-dom': { singleton: true, requiredVersion: false },
        '@apache-superset/core': { singleton: true, requiredVersion: false },
      },
    }),
  ],
});
