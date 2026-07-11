import React from 'react';

interface State {
  hasError: boolean;
  error: Error | null;
}

interface Props {
  children?: React.ReactNode;
}

class ErrorBoundary extends React.Component<Props, State> {
  state: State = { hasError: false, error: null };

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error };
  }

  handleReset = () => {
    this.setState({ hasError: false, error: null });
  };

  render() {
    if (this.state.hasError) {
      return (
        <div className="flex flex-col items-center justify-center min-h-[400px] text-center p-8">
          <div className="text-6xl mb-4">😵</div>
          <h2 className="text-xl font-bold text-gray-900 mb-2">页面出错了</h2>
          <p className="text-sm text-gray-500 mb-6 max-w-md">
            {this.state.error?.message || '发生了未知错误'}
          </p>
          <button
            onClick={this.handleReset}
            className="px-4 py-2 bg-indigo-600 text-white rounded-lg hover:bg-indigo-700 text-sm"
          >
            重试
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}

export default ErrorBoundary;
