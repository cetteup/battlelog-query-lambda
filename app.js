const { URLSearchParams } = require('url');
const fetch = require('node-fetch');

exports.lambdaHandler = async (event) => {
    // Init response
    let response = {
        headers: { 'Content-Type': 'application/json' }
    };

    // Try to fetch data from cache or source
    try {
        // Make sure a channel name has been provided
        if (!event?.queryStringParameters?.nick) {
            response.statusCode = 422;
            throw new Error('No/invalid player nick provided');
        }

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