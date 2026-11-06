const path = require('node:path');
const ts = require('typescript');

// Resolve authored keys, including finite template values and translation wrappers.
// Open-ended values from API responses still need the caller's runtime fallback.
function collectTranslationReferences(root, namespaces) {
  const config = ts.readConfigFile(path.join(root, 'tsconfig.json'), ts.sys.readFile);
  const parsed = ts.parseJsonConfigFileContent(config.config, ts.sys, root);
  const program = ts.createProgram(parsed.fileNames, parsed.options);
  const checker = program.getTypeChecker();
  const references = new Map();
  const keyPattern = /^[a-z_]+\.[\w.-]+$/;

  function literalValues(type) {
    if (type.isStringLiteral() || type.isNumberLiteral()) return [String(type.value)];
    if (type.isUnion()) {
      const values = type.types.map(literalValues);
      if (values.every(Boolean)) return values.flat();
    }
    return null;
  }

  function arrayValues(node, seen, bindings) {
    if (ts.isArrayLiteralExpression(node))
      return node.elements.flatMap(item => stringValues(item, seen, bindings));
    if (ts.isParenthesizedExpression(node) || ts.isAsExpression(node))
      return arrayValues(node.expression, seen, bindings);
    if (
      ts.isCallExpression(node) &&
      ts.isPropertyAccessExpression(node.expression) &&
      ['filter', 'slice'].includes(node.expression.name.text)
    )
      return arrayValues(node.expression.expression, seen, bindings);
    if (ts.isIdentifier(node)) {
      const declaration = checker.getSymbolAtLocation(node)?.valueDeclaration;
      if (declaration && ts.isVariableDeclaration(declaration) && declaration.initializer)
        return arrayValues(declaration.initializer, seen, bindings);
    }
    return [];
  }

  function stringValues(node, seen = new Set(), bindings = new Map()) {
    if (!node || seen.has(node)) return [];
    seen = new Set(seen).add(node);
    if (ts.isStringLiteralLike(node) || ts.isNumericLiteral(node)) return [node.text];
    if (ts.isConditionalExpression(node))
      return [node.whenTrue, node.whenFalse].flatMap(item => stringValues(item, seen, bindings));
    if (
      ts.isParenthesizedExpression(node) ||
      ts.isAsExpression(node) ||
      ts.isNonNullExpression(node)
    )
      return stringValues(node.expression, seen, bindings);
    if (ts.isTemplateExpression(node)) {
      let result = [node.head.text];
      for (const span of node.templateSpans) {
        const values = stringValues(span.expression, seen, bindings);
        if (!values.length) return [];
        result = result.flatMap(prefix => values.map(value => prefix + value + span.literal.text));
      }
      return result;
    }
    if (ts.isIdentifier(node) && bindings.has(node.text)) return bindings.get(node.text);
    const values = literalValues(checker.getTypeAtLocation(node));
    if (values) return values;
    if (ts.isIdentifier(node)) {
      const declaration = checker.getSymbolAtLocation(node)?.valueDeclaration;
      if (declaration && ts.isVariableDeclaration(declaration) && declaration.initializer)
        return stringValues(declaration.initializer, seen, bindings);
      if (declaration && ts.isParameter(declaration)) {
        const callback = declaration.parent;
        const call = callback.parent;
        if (
          ts.isArrowFunction(callback) &&
          ts.isCallExpression(call) &&
          ts.isPropertyAccessExpression(call.expression) &&
          call.expression.name.text === 'map'
        )
          return arrayValues(call.expression.expression, seen, bindings);
      }
    }
    return [];
  }

  function isTranslationCall(node) {
    return (
      ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 't'
    );
  }

  for (const source of program.getSourceFiles()) {
    const file = path.relative(root, source.fileName).split(path.sep).join('/');
    if (!file.startsWith('src/') || file.includes('/i18n/locales/') || /Messages\.ts$/.test(file))
      continue;
    const record = (keys, node) => {
      const line = source.getLineAndCharacterOfPosition(node.getStart()).line + 1;
      for (const key of keys)
        if (keyPattern.test(key) && !references.has(key)) references.set(key, `${file}:${line}`);
    };
    function visit(node) {
      // Also cover keys passed indirectly through navigation, help and language metadata.
      if (ts.isStringLiteralLike(node) && namespaces.has(node.text.split('.')[0]))
        record([node.text], node);
      if (isTranslationCall(node)) record(stringValues(node.arguments[0]), node);
      else if (ts.isCallExpression(node) && ts.isIdentifier(node.expression)) {
        const declaration = checker.getSymbolAtLocation(node.expression)?.valueDeclaration;
        const wrapper =
          declaration && ts.isVariableDeclaration(declaration) && declaration.initializer;
        if (wrapper && ts.isArrowFunction(wrapper) && isTranslationCall(wrapper.body)) {
          const bindings = new Map(
            wrapper.parameters.map((parameter, index) => [
              parameter.name.getText(),
              stringValues(node.arguments[index]),
            ]),
          );
          record(stringValues(wrapper.body.arguments[0], new Set(), bindings), node);
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(source);
  }
  return references;
}

module.exports = { collectTranslationReferences };
