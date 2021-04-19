const { URLSearchParams } = require('url');
const fetch = require('node-fetch');

exports.lambdaHandler = async (event) => {
    // Init response
    let response = {
        headers: { 'Content-Type': 'application/json' }
    };

    try {
        // Make sure a player name has been provided
        if (!event?.queryStringParameters?.nick) {
            response.statusCode = 422;
            throw new Error('No/invalid player nick provided');
        }

        // Remove trailing/leading whitespaces from name, convert to lower case and escape underscores
        // (Battlelog uses underscores as wildcard characters for the user search)
        const playerName = event.queryStringParameters.nick.trim().toLowerCase().replace(/_/g, '\\_');

        const params = new URLSearchParams();
        params.append('query', playerName);

        const srcResponse = await fetch('https://battlelog.battlefield.com/search/query/', {
            method: 'POST',
            body: params
        });

        // Finish setting up response
        response.statusCode = 200;
        response.body = await srcResponse.text();
    } catch (err) {
        console.log(err);
        response.statusCode = 500;
        response.body = JSON.stringify({ errors: [err.message] });
    }

    return response;
};